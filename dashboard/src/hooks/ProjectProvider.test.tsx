// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { ProjectProvider } from './ProjectProvider'
import { useProject } from './useProject'

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
  const { projects, activeProject } = useProject()

  return (
    <div>
      <span data-testid="project-count">{projects.length}</span>
      <span data-testid="active-project">{activeProject?.name || ''}</span>
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
})

function flushPromises() {
  return new Promise(resolve => setTimeout(resolve, 0))
}