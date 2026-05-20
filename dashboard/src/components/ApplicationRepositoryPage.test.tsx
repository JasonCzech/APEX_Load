// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import ApplicationRepositoryPage from './ApplicationRepositoryPage'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const apiMock = {
  listProjectReleases: vi.fn(),
  createRepositoryCategory: vi.fn(),
  createProjectRelease: vi.fn(),
}

vi.mock('../hooks/api/useRepositoryApi', () => ({
  useRepositoryApi: () => apiMock,
}))

const domain = { id: 'domain-1', name: 'Retail', created_at: '', updated_at: '' }
const repository = { id: 'repo-1', domain_id: domain.id, name: 'Application Repository', created_at: '', updated_at: '' }
const category = { id: 'category-1', domain_id: domain.id, repository_id: repository.id, name: 'Checkout', created_at: '', updated_at: '' }
const project = { id: 'project-1', domain_id: domain.id, category_id: category.id, name: 'Checkout API', created_at: '', updated_at: '' }

async function flushPromises() {
  await act(async () => {
    await Promise.resolve()
  })
}

function findButton(container: HTMLElement, label: string): HTMLButtonElement | undefined {
  return Array.from(container.querySelectorAll('button')).find(button => button.textContent?.trim() === label) as HTMLButtonElement | undefined
}

function setInputValue(input: HTMLInputElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')?.set
  setter?.call(input, value)
  input.dispatchEvent(new Event('input', { bubbles: true }))
}

describe('ApplicationRepositoryPage', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    apiMock.listProjectReleases.mockResolvedValue({ releases: [{ id: 'release-1', application_id: 'app-1', project_id: project.id, name: '2026.05', created_at: '', updated_at: '' }], count: 1 })
    apiMock.createRepositoryCategory.mockResolvedValue({ id: 'category-2', domain_id: domain.id, repository_id: repository.id, name: 'Search', created_at: '', updated_at: '' })
    apiMock.createProjectRelease.mockResolvedValue({ id: 'release-2', application_id: 'app-1', project_id: project.id, name: '2026.06', created_at: '', updated_at: '' })
  })

  afterEach(() => {
    vi.clearAllMocks()
    act(() => root.unmount())
    container.remove()
  })

  it('renders repository hierarchy and creates releases under a project', async () => {
    await act(async () => {
      root.render(
        <ApplicationRepositoryPage
          domains={[domain]}
          activeDomain={domain}
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

    expect(container.textContent).toContain('Application Repository')
    expect(container.textContent).toContain('Checkout')
    expect(container.textContent).toContain('Checkout API')
    expect(container.textContent).toContain('2026.05')

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

    expect(apiMock.createProjectRelease).toHaveBeenCalledWith(project.id, { name: '2026.06' })
    expect(container.textContent).toContain('Release created.')
  })
})
