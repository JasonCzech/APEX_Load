// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { ProjectProvider } from './ProjectProvider'
import { useProject } from './useProject'
import type { Project } from '../types'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const projectApiMock = {
  listProjects: vi.fn(),
  createProject: vi.fn(),
  updateProject: vi.fn(),
  deleteProject: vi.fn(),
  listFolders: vi.fn(),
  createFolder: vi.fn(),
  updateFolder: vi.fn(),
  deleteFolder: vi.fn(),
}

vi.mock('./api/useProjectApi', () => ({
  useProjectApi: () => projectApiMock,
}))

function Probe() {
  const { projects, activeProject, createAndSelectProject } = useProject()

  return (
    <div>
      <span data-testid="project-count">{projects.length}</span>
      <span data-testid="active-project">{activeProject?.name || ''}</span>
      <button type="button" onClick={() => { void createAndSelectProject('New Project') }}>Create project</button>
    </div>
  )
}

describe('ProjectProvider', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    localStorage.clear()

    projectApiMock.listProjects.mockResolvedValue({
      projects: [
        {
          id: 'project-1',
          name: 'Checkout',
          created_at: '2026-05-16T00:00:00Z',
          updated_at: '2026-05-16T00:00:00Z',
        },
      ],
      count: 1,
    })
    projectApiMock.createProject.mockResolvedValue({
      id: 'project-new',
      name: 'New Project',
      created_at: '2026-05-16T00:00:00Z',
      updated_at: '2026-05-16T00:00:00Z',
    })
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    vi.clearAllMocks()
  })

  it('loads projects once during initial activation', async () => {
    await act(async () => {
      root.render(
        <ProjectProvider>
          <Probe />
        </ProjectProvider>,
      )
    })

    await flushPromises()
    await flushPromises()

    expect(projectApiMock.listProjects).toHaveBeenCalledTimes(1)
    expect(container.querySelector('[data-testid="project-count"]')?.textContent).toBe('1')
    expect(container.querySelector('[data-testid="active-project"]')?.textContent).toBe('Checkout')
  })

  it('keeps the API-backed project active when browser storage is unavailable', async () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new DOMException('Storage disabled', 'SecurityError')
    })
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new DOMException('Storage disabled', 'SecurityError')
    })

    await act(async () => {
      root.render(<ProjectProvider><Probe /></ProjectProvider>)
    })
    await flushPromises()

    expect(container.querySelector('[data-testid="active-project"]')?.textContent).toBe('Checkout')
  })

  it('does not let an older refresh overwrite a newly created project', async () => {
    let resolveRefresh!: (value: { projects: Project[]; count: number }) => void
    projectApiMock.listProjects.mockImplementationOnce(() => new Promise(resolve => {
      resolveRefresh = resolve
    }))

    await act(async () => {
      root.render(<ProjectProvider><Probe /></ProjectProvider>)
      await Promise.resolve()
    })
    const createButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Create project')
    await act(async () => {
      createButton?.click()
      await Promise.resolve()
    })
    expect(container.querySelector('[data-testid="active-project"]')?.textContent).toBe('New Project')

    await act(async () => {
      resolveRefresh({
        projects: [{
          id: 'project-old',
          name: 'Old Project',
          created_at: '2026-05-16T00:00:00Z',
          updated_at: '2026-05-16T00:00:00Z',
        }],
        count: 1,
      })
      await Promise.resolve()
    })

    expect(container.querySelector('[data-testid="active-project"]')?.textContent).toBe('New Project')
    expect(container.querySelector('[data-testid="project-count"]')?.textContent).toBe('1')
  })
})

function flushPromises() {
  return new Promise(resolve => setTimeout(resolve, 0))
}
