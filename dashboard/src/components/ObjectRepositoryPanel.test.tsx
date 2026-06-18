// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import ObjectRepositoryPanel from './ObjectRepositoryPanel'
import { ObjectRepository } from '../types'

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
  setter.call(input, value)
  input.dispatchEvent(new Event('input', { bubbles: true }))
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

    expect(apiMock.createObjectRepositoryFromScript).toHaveBeenCalledWith({ script_id: 'script-7' })
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
})
