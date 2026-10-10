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

const repositoryApiMock = {
  createProjectRelease: vi.fn(),
}

const projectState = {
  activeProject: { id: 'project-1', name: 'Checkout', created_at: '', updated_at: '' },
}

vi.mock('./api/useApplicationApi', () => ({
  useApplicationApi: () => applicationApiMock,
}))

vi.mock('./api/useRepositoryApi', () => ({
  useRepositoryApi: () => repositoryApiMock,
}))

vi.mock('./useProject', () => ({
  useProject: () => projectState,
}))

function Probe() {
  const {
    applications,
    applicationsLoading,
    applicationsError,
    releases,
    releasesLoading,
    releasesError,
    activeApplication,
    activeRelease,
    setActiveApplication,
    setActiveRelease,
    removeApplication,
    removeRelease,
    refreshApplications,
    refreshReleases,
    createAndSelectApplication,
    createAndSelectRelease,
  } = useApplication()

  return (
    <div>
      <span data-testid="application-count">{applications.length}</span>
      <span data-testid="applications-loading">{String(applicationsLoading)}</span>
      <span data-testid="applications-error">{applicationsError}</span>
      <span data-testid="active-application">{activeApplication?.name || ''}</span>
      <span data-testid="releases">{releases.map(release => release.name).join(',')}</span>
      <span data-testid="releases-loading">{String(releasesLoading)}</span>
      <span data-testid="releases-error">{releasesError}</span>
      <span data-testid="active-release">{activeRelease?.name || ''}</span>
      {applications.map(application => (
        <button key={application.id} type="button" onClick={() => setActiveApplication(application)}>
          Select {application.name}
        </button>
      ))}
      {releases.map(release => (
        <button key={release.id} type="button" onClick={() => setActiveRelease(release)}>
          Select release {release.name}
        </button>
      ))}
      <button type="button" onClick={() => createAndSelectApplication('New App')}>Create</button>
      <button type="button" onClick={() => void createAndSelectRelease('New Release')}>Create release</button>
      <button type="button" disabled={!activeApplication} onClick={() => activeApplication && removeApplication(activeApplication.id)}>Remove active application</button>
      <button type="button" disabled={!activeRelease} onClick={() => activeRelease && removeRelease(activeRelease.id)}>Remove active release</button>
      <button
        type="button"
        disabled={!activeApplication}
        onClick={() => activeApplication && setActiveApplication({
          ...activeApplication,
          name: 'Renamed Application',
          updated_at: '2026-05-16T00:00:00Z',
        })}
      >
        Apply application response
      </button>
      <button
        type="button"
        disabled={!activeRelease}
        onClick={() => activeRelease && setActiveRelease({
          ...activeRelease,
          name: 'Renamed Release',
          updated_at: '2026-05-16T00:00:00Z',
        })}
      >
        Apply release response
      </button>
      <button
        type="button"
        disabled={!activeApplication || !releases.some(release => release.id !== activeRelease?.id)}
        onClick={() => {
          const inactive = releases.find(release => release.id !== activeRelease?.id)
          if (!inactive || !activeApplication) return // the button is disabled otherwise
          void refreshReleases(activeApplication.id, {
            preserveRelease: {
              ...inactive,
              name: 'Renamed Inactive Release',
              updated_at: '2026-05-16T00:00:00Z',
            },
          }).catch(() => undefined)
        }}
      >
        Apply inactive release response
      </button>
      <button type="button" onClick={() => void refreshApplications().catch(() => undefined)}>Retry applications</button>
      <button type="button" onClick={() => void refreshReleases().catch(() => undefined)}>Retry releases</button>
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
    repositoryApiMock.createProjectRelease.mockResolvedValue(release('release-new', 'app-1', 'New Release'))
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

  it('applies an authoritative release mutation to both selection and catalog', async () => {
    applicationApiMock.listReleases.mockResolvedValue({
      releases: [release('release-1', 'app-1', 'Original Release')],
      count: 1,
    })

    await act(async () => {
      root.render(<ApplicationProvider><Probe /></ApplicationProvider>)
    })
    await flushPromises()
    await flushPromises()

    const applyButton = Array.from(container.querySelectorAll('button'))
      .find(button => button.textContent === 'Apply release response')
    await act(async () => {
      applyButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.querySelector('[data-testid="active-release"]')?.textContent).toBe('Renamed Release')
    expect(container.querySelector('[data-testid="releases"]')?.textContent).toBe('Renamed Release')
  })

  it('keeps an authoritative application rename through a stale successful list and accepts a newer snapshot', async () => {
    const original = application('app-1', 'project-1', 'Payments')
    const newer = {
      ...original,
      name: 'Server Final Name',
      updated_at: '2026-05-17T00:00:00Z',
    }
    applicationApiMock.listApplications
      .mockResolvedValueOnce({ applications: [original], count: 1 })
      .mockResolvedValueOnce({ applications: [original], count: 1 })
      .mockResolvedValueOnce({ applications: [newer], count: 1 })

    await act(async () => {
      root.render(<ApplicationProvider><Probe /></ApplicationProvider>)
    })
    await flushPromises()

    const apply = Array.from(container.querySelectorAll('button'))
      .find(button => button.textContent === 'Apply application response')
    const retry = Array.from(container.querySelectorAll('button'))
      .find(button => button.textContent === 'Retry applications')
    await act(async () => apply?.click())
    expect(readTestId('active-application')).toBe('Renamed Application')

    await act(async () => retry?.click())
    await flushPromises()
    expect(readTestId('active-application')).toBe('Renamed Application')

    await act(async () => retry?.click())
    await flushPromises()
    expect(readTestId('active-application')).toBe('Server Final Name')
  })

  it('keeps an authoritative release rename through a stale successful list and accepts a newer snapshot', async () => {
    const original = release('release-1', 'app-1', 'Payments 1.0')
    const newer = {
      ...original,
      name: 'Payments 1.0 Final',
      updated_at: '2026-05-17T00:00:00Z',
    }
    applicationApiMock.listReleases
      .mockResolvedValueOnce({ releases: [original], count: 1 })
      .mockResolvedValueOnce({ releases: [original], count: 1 })
      .mockResolvedValueOnce({ releases: [newer], count: 1 })

    await act(async () => {
      root.render(<ApplicationProvider><Probe /></ApplicationProvider>)
    })
    await flushPromises()
    await flushPromises()

    const apply = Array.from(container.querySelectorAll('button'))
      .find(button => button.textContent === 'Apply release response')
    const retry = Array.from(container.querySelectorAll('button'))
      .find(button => button.textContent === 'Retry releases')
    await act(async () => apply?.click())
    expect(readTestId('active-release')).toBe('Renamed Release')

    await act(async () => retry?.click())
    await flushPromises()
    expect(readTestId('active-release')).toBe('Renamed Release')

    await act(async () => retry?.click())
    await flushPromises()
    expect(readTestId('active-release')).toBe('Payments 1.0 Final')
  })

  it('keeps an inactive release mutation in the shared catalog through stale reconciliation', async () => {
    const active = release('release-1', 'app-1', 'Payments 1.0')
    const inactive = release('release-2', 'app-1', 'Payments 2.0')
    applicationApiMock.listReleases
      .mockResolvedValueOnce({ releases: [active, inactive], count: 2 })
      .mockResolvedValueOnce({ releases: [active, inactive], count: 2 })

    await act(async () => {
      root.render(<ApplicationProvider><Probe /></ApplicationProvider>)
    })
    await flushPromises()
    await flushPromises()

    const apply = Array.from(container.querySelectorAll('button'))
      .find(button => button.textContent === 'Apply inactive release response')
    await act(async () => apply?.click())
    await flushPromises()

    expect(readTestId('active-release')).toBe(active.name)
    expect(readTestId('releases')).toBe(`${active.name},Renamed Inactive Release`)
  })

  it('keeps a created application selected when it supersedes an older list snapshot', async () => {
    const staleList = deferred<Awaited<ReturnType<typeof applicationApiMock.listApplications>>>()
    const existing = application('app-1', 'project-1', 'Payments')
    const created = application('app-2', 'project-1', 'New App')
    applicationApiMock.createApplication.mockResolvedValue(created)
    applicationApiMock.listApplications
      .mockImplementationOnce(() => staleList.promise)
      // Model a read replica/eventual snapshot that has not observed the
      // successful create yet. Reconciliation must still retain that row.
      .mockResolvedValueOnce({ applications: [existing], count: 1 })

    await act(async () => {
      root.render(<ApplicationProvider><Probe /></ApplicationProvider>)
    })

    const createButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Create')
    await act(async () => {
      createButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(applicationApiMock.listApplications).toHaveBeenCalledTimes(2)
    expect(readTestId('application-count')).toBe('2')
    expect(readTestId('active-application')).toBe('New App')

    await act(async () => {
      staleList.resolve({ applications: [existing], count: 1 })
    })
    await flushPromises()

    expect(readTestId('application-count')).toBe('2')
    expect(readTestId('active-application')).toBe('New App')
  })

  it('does not synthesize an application when the server returns an empty hierarchy', async () => {
    applicationApiMock.listApplications.mockResolvedValue({ applications: [], count: 0 })

    await act(async () => {
      root.render(<ApplicationProvider><Probe /></ApplicationProvider>)
    })
    await flushPromises()

    expect(readTestId('application-count')).toBe('0')
    expect(readTestId('active-application')).toBe('')
    expect(applicationApiMock.createApplication).not.toHaveBeenCalled()
  })

  it('surfaces application load failures, preserves valid state, and retries', async () => {
    await act(async () => {
      root.render(<ApplicationProvider><Probe /></ApplicationProvider>)
    })
    await flushPromises()
    expect(readTestId('active-application')).toBe('Payments')

    applicationApiMock.listApplications.mockRejectedValueOnce(new Error('application service unavailable'))
    const retry = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Retry applications')
    await act(async () => {
      retry?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(readTestId('applications-loading')).toBe('false')
    expect(readTestId('applications-error')).toBe('Unable to load applications: application service unavailable')
    expect(readTestId('active-application')).toBe('Payments')
    expect(readTestId('application-count')).toBe('1')

    await act(async () => {
      retry?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()
    expect(readTestId('applications-error')).toBe('')
    expect(readTestId('active-application')).toBe('Payments')
  })

  it('applies an application tombstone and fallback before stale reconciliation', async () => {
    const payments = application('app-1', 'project-1', 'Payments')
    const catalog = application('app-2', 'project-1', 'Catalog')
    applicationApiMock.listApplications.mockResolvedValue({ applications: [payments, catalog], count: 2 })

    await act(async () => {
      root.render(<ApplicationProvider><Probe /></ApplicationProvider>)
    })
    await flushPromises()
    expect(readTestId('active-application')).toBe('Payments')

    const remove = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Remove active application')
    await act(async () => remove?.click())
    expect(readTestId('application-count')).toBe('1')
    expect(readTestId('active-application')).toBe('Catalog')

    const retry = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Retry applications')
    await act(async () => retry?.click())
    await flushPromises()

    expect(readTestId('application-count')).toBe('1')
    expect(readTestId('active-application')).toBe('Catalog')
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

  it('preserves the selected release family when switching applications', async () => {
    applicationApiMock.listApplications.mockResolvedValue({
      applications: [
        application('app-1', 'project-1', 'Payments'),
        application('app-2', 'project-1', 'Catalog'),
      ],
      count: 2,
    })
    const familyAApp1 = { ...release('release-a-1', 'app-1', '2607'), group_id: 'family-a' }
    const familyBApp1 = { ...release('release-b-1', 'app-1', '2608'), group_id: 'family-b' }
    const familyAApp2 = { ...release('release-a-2', 'app-2', '2607'), group_id: 'family-a' }
    const familyBApp2 = { ...release('release-b-2', 'app-2', '2608'), group_id: 'family-b' }
    applicationApiMock.listReleases.mockImplementation((applicationId: string) => Promise.resolve({
      releases: applicationId === 'app-1' ? [familyAApp1, familyBApp1] : [familyAApp2, familyBApp2],
      count: 2,
    }))

    await act(async () => {
      root.render(<ApplicationProvider><Probe /></ApplicationProvider>)
    })
    await flushPromises()
    await flushPromises()

    const select2608 = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Select release 2608')
    await act(async () => select2608?.click())
    expect(readTestId('active-release')).toBe('2608')

    const selectCatalog = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Select Catalog')
    await act(async () => selectCatalog?.click())
    await flushPromises()

    expect(readTestId('active-application')).toBe('Catalog')
    expect(readTestId('active-release')).toBe('2608')
    expect(localStorage.getItem('apexload.activeReleaseId')).toBe('family-b')
  })

  it('keeps the selected release when the active application is selected again', async () => {
    applicationApiMock.listReleases.mockResolvedValue({
      releases: [release('release-1', 'app-1', 'Payments 1.0')],
      count: 1,
    })

    await act(async () => {
      root.render(<ApplicationProvider><Probe /></ApplicationProvider>)
    })
    await flushPromises()
    expect(readTestId('active-release')).toBe('Payments 1.0')

    const selectPayments = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Select Payments')
    await act(async () => {
      selectPayments?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(applicationApiMock.listReleases).toHaveBeenCalledTimes(1)
    expect(readTestId('active-release')).toBe('Payments 1.0')
  })

  it('surfaces a release load failure and recovers through the shared retry path', async () => {
    applicationApiMock.listReleases
      .mockRejectedValueOnce(new Error('release service unavailable'))
      .mockResolvedValueOnce({
        releases: [release('release-1', 'app-1', 'Payments 1.0')],
        count: 1,
      })

    await act(async () => {
      root.render(<ApplicationProvider><Probe /></ApplicationProvider>)
    })
    await flushPromises()

    expect(readTestId('releases-loading')).toBe('false')
    expect(readTestId('releases-error')).toBe('Unable to load releases: release service unavailable')
    expect(readTestId('active-release')).toBe('')

    const retry = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Retry releases')
    await act(async () => {
      retry?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(applicationApiMock.listReleases).toHaveBeenCalledTimes(2)
    expect(readTestId('releases-error')).toBe('')
    expect(readTestId('active-release')).toBe('Payments 1.0')
  })

  it('applies a release tombstone and selects a valid fallback before refresh', async () => {
    const first = release('release-1', 'app-1', 'Payments 1.0')
    const second = release('release-2', 'app-1', 'Payments 2.0')
    applicationApiMock.listReleases.mockResolvedValue({ releases: [first, second], count: 2 })

    await act(async () => {
      root.render(<ApplicationProvider><Probe /></ApplicationProvider>)
    })
    await flushPromises()
    await flushPromises()
    expect(readTestId('active-release')).toBe('Payments 1.0')

    const remove = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Remove active release')
    await act(async () => remove?.click())
    expect(readTestId('releases')).toBe('Payments 2.0')
    expect(readTestId('active-release')).toBe('Payments 2.0')

    const retry = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Retry releases')
    await act(async () => retry?.click())
    await flushPromises()
    expect(readTestId('releases')).toBe('Payments 2.0')
    expect(readTestId('active-release')).toBe('Payments 2.0')
  })

  it('reconciles all releases when creation supersedes an in-flight list', async () => {
    const staleList = deferred<Awaited<ReturnType<typeof applicationApiMock.listReleases>>>()
    const existing = release('release-existing', 'app-1', 'Existing Release')
    const created = release('release-new', 'app-1', 'New Release')
    repositoryApiMock.createProjectRelease.mockResolvedValue(created)
    applicationApiMock.listReleases
      .mockImplementationOnce(() => staleList.promise)
      .mockResolvedValueOnce({ releases: [existing], count: 1 })

    await act(async () => {
      root.render(<ApplicationProvider><Probe /></ApplicationProvider>)
    })
    await flushPromises()

    const createRelease = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Create release')
    await act(async () => {
      createRelease?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(applicationApiMock.listReleases).toHaveBeenCalledTimes(2)
    expect(repositoryApiMock.createProjectRelease).toHaveBeenCalledWith('project-1', {
      name: 'New Release',
      application_id: 'app-1',
    })
    expect(readTestId('releases')).toBe('Existing Release,New Release')
    expect(readTestId('active-release')).toBe('New Release')

    await act(async () => {
      staleList.resolve({ releases: [existing], count: 1 })
    })
    await flushPromises()
    expect(readTestId('releases')).toBe('Existing Release,New Release')
    expect(readTestId('active-release')).toBe('New Release')
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
