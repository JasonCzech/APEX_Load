// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { ApplicationProvider, useApplication } from './useApplication'

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
  const { applications, activeApplication, createAndSelectApplication } = useApplication()

  return (
    <div>
      <span data-testid="application-count">{applications.length}</span>
      <span data-testid="active-application">{activeApplication?.name || ''}</span>
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

    const createButton = container.querySelector('button')
    expect(createButton).toBeTruthy()
    await act(async () => {
      createButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    expect(applicationApiMock.createApplication).toHaveBeenCalledWith({ name: 'New App', project_id: 'project-1' })
  })
})

function flushPromises() {
  return new Promise(resolve => setTimeout(resolve, 0))
}