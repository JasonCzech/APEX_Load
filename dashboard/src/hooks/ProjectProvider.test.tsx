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
	const { projects, projectsLoading, projectsError, activeProject, setActiveProject, removeProject, refreshProjects, createAndSelectProject, updateProject, deleteProject } = useProject()

  return (
    <div>
      <span data-testid="project-count">{projects.length}</span>
      <span data-testid="projects">{projects.map(project => project.name).join(',')}</span>
      <span data-testid="active-project">{activeProject?.name || ''}</span>
      <span data-testid="projects-loading">{String(projectsLoading)}</span>
      <span data-testid="projects-error">{projectsError}</span>
      <button type="button" onClick={() => { void createAndSelectProject('New Project') }}>Create project</button>
      <button type="button" onClick={() => { void refreshProjects().catch(() => undefined) }}>Refresh projects</button>
			<button type="button" disabled={!activeProject} onClick={() => { if (activeProject) void updateProject(activeProject.id, { name: 'Updated Project' }) }}>Update project</button>
			<button type="button" disabled={!activeProject} onClick={() => { if (activeProject) void deleteProject(activeProject.id).catch(() => undefined) }}>Delete project</button>
			<button type="button" disabled={!activeProject} onClick={() => { if (activeProject) removeProject(activeProject.id) }}>Remove project locally</button>
      <button
        type="button"
        disabled={!activeProject}
        onClick={() => activeProject && setActiveProject({ ...activeProject, name: 'Renamed Project' })}
      >
        Apply project response
      </button>
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
		projectApiMock.updateProject.mockResolvedValue({
			id: 'project-1',
			name: 'Updated Project',
			created_at: '2026-05-16T00:00:00Z',
			updated_at: '2026-05-17T00:00:00Z',
		})
		projectApiMock.deleteProject.mockResolvedValue({ deleted: 'project-1' })
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

  it('applies an authoritative active-project mutation to the catalog row', async () => {
    await act(async () => {
      root.render(<ProjectProvider><Probe /></ProjectProvider>)
    })
    await flushPromises()

    const applyButton = Array.from(container.querySelectorAll('button'))
      .find(button => button.textContent === 'Apply project response')
    await act(async () => {
      applyButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.querySelector('[data-testid="active-project"]')?.textContent).toBe('Renamed Project')
    expect(container.querySelector('[data-testid="projects"]')?.textContent).toBe('Renamed Project')
  })

  it('does not invent a client-only project when the catalog request fails', async () => {
    projectApiMock.listProjects.mockRejectedValueOnce(new Error('catalog unavailable'))

    await act(async () => {
      root.render(<ProjectProvider><Probe /></ProjectProvider>)
    })
    await flushPromises()

    expect(container.querySelector('[data-testid="project-count"]')?.textContent).toBe('0')
    expect(container.querySelector('[data-testid="active-project"]')?.textContent).toBe('')
    expect(container.querySelector('[data-testid="projects-loading"]')?.textContent).toBe('false')
    expect(container.querySelector('[data-testid="projects-error"]')?.textContent).toContain('catalog unavailable')
  })

  it('does not create a project as a side effect of an empty catalog read', async () => {
    projectApiMock.listProjects.mockResolvedValueOnce({ projects: [], count: 0 })

    await act(async () => {
      root.render(<ProjectProvider><Probe /></ProjectProvider>)
    })
    await flushPromises()

    expect(projectApiMock.createProject).not.toHaveBeenCalled()
    expect(container.querySelector('[data-testid="project-count"]')?.textContent).toBe('0')
    expect(container.querySelector('[data-testid="active-project"]')?.textContent).toBe('')
  })

  it('preserves the last successful project catalog when a later refresh fails', async () => {
    await act(async () => {
      root.render(<ProjectProvider><Probe /></ProjectProvider>)
    })
    await flushPromises()
    expect(container.querySelector('[data-testid="active-project"]')?.textContent).toBe('Checkout')

    projectApiMock.listProjects.mockRejectedValueOnce(new Error('temporary catalog failure'))
    const refreshButton = Array.from(container.querySelectorAll('button'))
      .find(button => button.textContent === 'Refresh projects')
    await act(async () => {
      refreshButton?.click()
    })
    await flushPromises()

    expect(container.querySelector('[data-testid="projects"]')?.textContent).toBe('Checkout')
    expect(container.querySelector('[data-testid="active-project"]')?.textContent).toBe('Checkout')
    expect(container.querySelector('[data-testid="projects-error"]')?.textContent).toContain('temporary catalog failure')
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

	it('keeps authoritative create and update responses over later stale successful lists', async () => {
		await act(async () => {
			root.render(<ProjectProvider><Probe /></ProjectProvider>)
		})
		await flushPromises()

		await act(async () => {
			Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Create project')?.click()
		})
		await flushPromises()
		expect(container.querySelector('[data-testid="projects"]')?.textContent).toContain('New Project')

		projectApiMock.updateProject.mockResolvedValueOnce({
			id: 'project-new',
			name: 'Updated Project',
			created_at: '2026-05-16T00:00:00Z',
			updated_at: '2026-05-17T00:00:00Z',
		})
		await act(async () => {
			Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Update project')?.click()
		})
		await flushPromises()
		expect(container.querySelector('[data-testid="active-project"]')?.textContent).toBe('Updated Project')

		projectApiMock.listProjects.mockResolvedValueOnce({
			projects: [
				{ id: 'project-1', name: 'Checkout', created_at: '2026-05-16T00:00:00Z', updated_at: '2026-05-16T00:00:00Z' },
				{ id: 'project-new', name: 'New Project', created_at: '2026-05-16T00:00:00Z', updated_at: '2026-05-16T00:00:00Z' },
			],
			count: 2,
		})
		await act(async () => {
			Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Refresh projects')?.click()
		})
		await flushPromises()

		expect(container.querySelector('[data-testid="projects"]')?.textContent).toContain('Updated Project')
		expect(container.querySelector('[data-testid="projects"]')?.textContent).not.toContain('New Project')
		expect(container.querySelector('[data-testid="active-project"]')?.textContent).toBe('Updated Project')
	})

	it('keeps a deleted project removed through failed and stale successful reconciliation reads', async () => {
		projectApiMock.listProjects.mockResolvedValueOnce({
			projects: [
				{ id: 'project-1', name: 'Checkout', created_at: '', updated_at: '2026-05-16T00:00:00Z' },
				{ id: 'project-2', name: 'Payments', created_at: '', updated_at: '2026-05-16T00:00:00Z' },
			],
			count: 2,
		})
		await act(async () => {
			root.render(<ProjectProvider><Probe /></ProjectProvider>)
		})
		await flushPromises()

		await act(async () => {
			Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Delete project')?.click()
		})
		await flushPromises()
		expect(container.querySelector('[data-testid="projects"]')?.textContent).toBe('Payments')
		expect(container.querySelector('[data-testid="active-project"]')?.textContent).toBe('Payments')

		projectApiMock.listProjects.mockRejectedValueOnce(new Error('project refresh unavailable'))
		await act(async () => {
			Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Refresh projects')?.click()
		})
		await flushPromises()
		expect(container.querySelector('[data-testid="projects"]')?.textContent).toBe('Payments')
		expect(container.querySelector('[data-testid="projects-error"]')?.textContent).toContain('project refresh unavailable')

		projectApiMock.listProjects.mockResolvedValueOnce({
			projects: [
				{ id: 'project-1', name: 'Checkout', created_at: '', updated_at: '2026-05-16T00:00:00Z' },
				{ id: 'project-2', name: 'Payments', created_at: '', updated_at: '2026-05-16T00:00:00Z' },
			],
			count: 2,
		})
		await act(async () => {
			Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Refresh projects')?.click()
		})
		await flushPromises()
		expect(container.querySelector('[data-testid="projects"]')?.textContent).toBe('Payments')
		expect(container.querySelector('[data-testid="active-project"]')?.textContent).toBe('Payments')
	})
})

function flushPromises() {
  return new Promise(resolve => setTimeout(resolve, 0))
}
