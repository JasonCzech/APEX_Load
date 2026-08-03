// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import ObjectRepositoryPanel from './ObjectRepositoryPanel'
import { ObjectRepository } from '../types'
import { ApiError } from '../hooks/apiClient'
import { NavigationGuardRegisterContext, type NavigationGuard } from '../hooks/NavigationGuardProvider'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const apiMock = {
  listObjectRepositories: vi.fn(),
  getObjectRepository: vi.fn(),
  createObjectRepository: vi.fn(),
  updateObjectRepository: vi.fn(),
  deleteObjectRepository: vi.fn(),
  createObjectRepositoryFromScript: vi.fn(),
}

vi.mock('../hooks/api/useObjectRepositoryApi', () => ({
  useObjectRepositoryApi: () => apiMock,
}))

let container: HTMLElement
let root: Root

beforeEach(() => {
  container = document.createElement('div')
  document.body.appendChild(container)
  root = createRoot(container)
})
afterEach(() => {
  act(() => root.unmount())
  container.remove()
  vi.clearAllMocks()
})

async function flush() {
  await act(async () => { await Promise.resolve(); await Promise.resolve() })
}

function repo(overrides: Partial<ObjectRepository>): ObjectRepository {
  return { id: 'r1', project_id: 'proj-a', name: 'Checkout', objects: [], created_at: '', updated_at: '', ...overrides }
}

function findButton(text: string): HTMLButtonElement | undefined {
  return Array.from(container.querySelectorAll('button')).find(b => b.textContent?.trim() === text) as HTMLButtonElement | undefined
}

function setInput(placeholder: string, value: string) {
  const input = Array.from(container.querySelectorAll('input')).find(i => i.getAttribute('placeholder') === placeholder) as HTMLInputElement
  const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')!.set!
  act(() => {
    setter.call(input, value)
    input.dispatchEvent(new Event('input', { bubbles: true }))
  })
}

describe('ObjectRepositoryPanel', () => {
  it('lists repositories and seeds one from a script', async () => {
    apiMock.listObjectRepositories.mockResolvedValue({ object_repositories: [repo({ objects: [{ logical_name: 'go', css: '#go', best_confidence: 90 }] })], count: 1 })
    apiMock.createObjectRepositoryFromScript.mockResolvedValue(repo({ id: 'r2', name: 'Seeded' }))

    await act(async () => { root.render(<ObjectRepositoryPanel projectId="proj-a" canManage />) })
    await flush()

    expect(container.textContent).toContain('Checkout')
    expect(container.textContent).toContain('1 object')

    setInput('Seed from script id…', 'script-7')
    await act(async () => { findButton('Import from script')!.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    await flush()

    expect(apiMock.createObjectRepositoryFromScript).toHaveBeenCalledWith({ project_id: 'proj-a', script_id: 'script-7' })
  })

  it('creates an empty repository', async () => {
    apiMock.listObjectRepositories.mockResolvedValue({ object_repositories: [], count: 0 })
    apiMock.createObjectRepository.mockResolvedValue(repo({ name: 'New' }))

    await act(async () => { root.render(<ObjectRepositoryPanel projectId="proj-a" canManage />) })
    await flush()
    expect(container.textContent).toContain('No object repositories yet.')

    await act(async () => { findButton('New repository')!.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    setInput('Repository name', 'Login objects')
    await act(async () => { findButton('Create')!.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    await flush()

    expect(apiMock.createObjectRepository).toHaveBeenCalledWith(expect.objectContaining({ project_id: 'proj-a', name: 'Login objects' }))
  })

  it('prompts to select a project when none is active', async () => {
    await act(async () => { root.render(<ObjectRepositoryPanel canManage />) })
    await flush()
    expect(container.textContent).toContain('Select a project')
    expect(apiMock.listObjectRepositories).not.toHaveBeenCalled()
  })

  it('shows ranked candidates (flagging self-heals) and promotes one to primary', async () => {
    apiMock.listObjectRepositories.mockResolvedValue({ object_repositories: [repo({
      objects: [{
        logical_name: 'Login', css: '#login', best_confidence: 88,
        candidates: [
          { kind: 'css', value: '#login', confidence: 88, reason: 'captured css' },
          { kind: 'xpath', value: 'xpath=//a', confidence: 58, reason: 'observed via replay self-heal' },
        ],
      }],
    })], count: 1 })
    apiMock.updateObjectRepository.mockResolvedValue(repo({}))

    await act(async () => { root.render(<ObjectRepositoryPanel projectId="proj-a" canManage />) })
    await flush()

    // Expand the repository to reveal its objects + candidates.
    const title = container.querySelector('.pp-row-title-button') as HTMLButtonElement
    await act(async () => { title.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    await flush()

    expect(container.textContent).toContain('xpath=//a')
    expect(container.textContent).toContain('self-healed')

    // Promote the non-primary (self-healed) candidate.
    await act(async () => { findButton('Promote')!.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    await flush()

    expect(apiMock.updateObjectRepository).toHaveBeenCalledTimes(1)
    const body = apiMock.updateObjectRepository.mock.calls[0][1] as ObjectRepository
    const object = body.objects[0]
    expect(object.xpath).toBe('xpath=//a') // mirrored into the descriptor
    expect(object.best_confidence).toBe(89) // bumped strictly above the prior primary
    expect(object.candidates?.find(c => c.value === 'xpath=//a')?.confidence).toBe(89)
  })

  it('passes bind_script when the import checkbox is set', async () => {
    apiMock.listObjectRepositories.mockResolvedValue({ object_repositories: [], count: 0 })
    apiMock.createObjectRepositoryFromScript.mockResolvedValue(repo({ id: 'r9' }))

    await act(async () => { root.render(<ObjectRepositoryPanel projectId="proj-a" canManage />) })
    await flush()

    setInput('Seed from script id…', 'script-9')
    const checkbox = container.querySelector('.pp-check input[type=checkbox]') as HTMLInputElement
    await act(async () => { checkbox.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    await act(async () => { findButton('Import from script')!.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    await flush()

    expect(apiMock.createObjectRepositoryFromScript).toHaveBeenCalledWith(expect.objectContaining({ script_id: 'script-9', bind_script: true }))
  })

  it('ignores a late object-repository response from the previous project', async () => {
    let resolveProjectA: ((value: { object_repositories: ObjectRepository[]; count: number }) => void) | undefined
    apiMock.listObjectRepositories.mockImplementation(({ project_id }: { project_id: string }) => {
      if (project_id === 'proj-a') return new Promise(resolve => { resolveProjectA = resolve })
      return Promise.resolve({ object_repositories: [repo({ id: 'r-b', project_id: 'proj-b', name: 'Project B Repository' })], count: 1 })
    })
    await act(async () => { root.render(<ObjectRepositoryPanel projectId="proj-a" canManage />) })
    await act(async () => { root.render(<ObjectRepositoryPanel projectId="proj-b" canManage />) })
    await flush()
    expect(container.textContent).toContain('Project B Repository')

    await act(async () => {
      resolveProjectA?.({ object_repositories: [repo({ name: 'Stale Project A Repository' })], count: 1 })
    })
    await flush()
    expect(container.textContent).toContain('Project B Repository')
    expect(container.textContent).not.toContain('Stale Project A Repository')
  })

  it('clears create and import drafts when the project changes', async () => {
    apiMock.listObjectRepositories.mockResolvedValue({ object_repositories: [], count: 0 })
    await act(async () => { root.render(<ObjectRepositoryPanel projectId="proj-a" canManage />) })
    await flush()
    await act(async () => findButton('New repository')?.click())
    setInput('Repository name', 'Project A draft')
    setInput('Seed from script id…', 'project-a-script')

    await act(async () => { root.render(<ObjectRepositoryPanel projectId="proj-b" canManage />) })
    await flush()
    expect(container.querySelector('input[placeholder="Repository name"]')).toBeNull()
    expect(container.querySelector<HTMLInputElement>('input[placeholder="Seed from script id…"]')?.value).toBe('')
    await act(async () => findButton('New repository')?.click())
    expect(container.querySelector<HTMLInputElement>('input[placeholder="Repository name"]')?.value).toBe('')
  })

  it('keeps the last good repository list when a same-project refresh fails', async () => {
    apiMock.listObjectRepositories.mockResolvedValueOnce({ object_repositories: [repo({ name: 'Retained Repository' })], count: 1 })
    apiMock.updateObjectRepository.mockResolvedValue(repo({ name: 'Retained Repository', auto_promote_on_heal: true }))
    await act(async () => { root.render(<ObjectRepositoryPanel projectId="proj-a" canManage />) })
    await flush()
    apiMock.listObjectRepositories.mockRejectedValueOnce(new ApiError('repository refresh unavailable', 503))
    const checkbox = container.querySelector<HTMLInputElement>('.pp-row-actions input[type="checkbox"]')
    await act(async () => checkbox?.click())
    await flush()
    expect(container.textContent).toContain('Retained Repository')
    expect(container.textContent).toContain('repository refresh unavailable')
  })

  it('loads every repository page instead of truncating the catalog at 100', async () => {
    const firstPage = Array.from({ length: 100 }, (_, index) => repo({ id: `repo-${index}`, name: `Repository ${index}` }))
    apiMock.listObjectRepositories
      .mockResolvedValueOnce({ object_repositories: firstPage, count: 100, limit: 100, offset: 0 })
      .mockResolvedValueOnce({ object_repositories: [repo({ id: 'repo-100', name: 'Repository 100' })], count: 1, limit: 100, offset: 100 })

    await act(async () => root.render(<ObjectRepositoryPanel projectId="proj-a" canManage />))
    await flush()

    expect(apiMock.listObjectRepositories).toHaveBeenNthCalledWith(1, { project_id: 'proj-a', limit: 100, offset: 0 })
    expect(apiMock.listObjectRepositories).toHaveBeenNthCalledWith(2, { project_id: 'proj-a', limit: 100, offset: 100 })
    expect(container.textContent).toContain('Repository 100')
  })

  it('locks rapid imports and rejects a response owned by another project', async () => {
    let resolveImport!: (value: ObjectRepository) => void
    apiMock.listObjectRepositories.mockResolvedValue({ object_repositories: [], count: 0 })
    apiMock.createObjectRepositoryFromScript.mockReturnValue(new Promise(resolve => { resolveImport = resolve }))
    await act(async () => root.render(<ObjectRepositoryPanel projectId="proj-a" canManage />))
    await flush()
    setInput('Seed from script id…', 'script-b')
    const importButton = findButton('Import from script')!
    act(() => {
      importButton.click()
      importButton.click()
    })
    expect(apiMock.createObjectRepositoryFromScript).toHaveBeenCalledTimes(1)

    await act(async () => {
      resolveImport(repo({ id: 'repo-b', project_id: 'proj-b', name: 'Project B objects' }))
      await Promise.resolve()
    })
    await flush()

    expect(container.textContent).toContain('different project')
    expect(container.textContent).not.toContain('Project B objects')
    expect(container.querySelector<HTMLInputElement>('input[placeholder="Seed from script id…"]')?.value).toBe('script-b')
  })

  it('registers create and import edits with the global navigation guard', async () => {
    let registration: { guard: NavigationGuard; dirty: boolean } | undefined
    apiMock.listObjectRepositories.mockResolvedValue({ object_repositories: [], count: 0 })
    await act(async () => {
      root.render(
        <NavigationGuardRegisterContext.Provider value={(guard, dirty) => {
          registration = { guard, dirty }
          return () => undefined
        }}>
          <ObjectRepositoryPanel projectId="proj-a" canManage />
        </NavigationGuardRegisterContext.Provider>,
      )
    })
    await flush()
    setInput('Seed from script id…', 'script-draft')
    await flush()
    expect(registration?.dirty).toBe(true)

    act(() => registration?.guard.discard())
    expect(container.querySelector<HTMLInputElement>('input[placeholder="Seed from script id…"]')?.value).toBe('')
  })
})
