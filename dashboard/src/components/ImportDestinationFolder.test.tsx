// @vitest-environment jsdom

import { act, useState } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import ImportDestinationFolder from './ImportDestinationFolder'
import { ApiError } from '../hooks/apiClient'

const apiMock = {
  listFolders: vi.fn(),
  getAuthEffectiveAccess: vi.fn(),
  createFolder: vi.fn(),
}

vi.mock('../hooks/useApi', () => ({
  useApi: () => apiMock,
}))

async function flushPromises() {
  await act(async () => {
    await Promise.resolve()
    await Promise.resolve()
  })
}

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise
    reject = rejectPromise
  })
  return { promise, resolve, reject }
}

describe('ImportDestinationFolder', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    vi.clearAllMocks()
    apiMock.getAuthEffectiveAccess.mockResolvedValue({
      resources: [{
        id: 'projects_and_folders',
        operations: [{ id: 'manage_projects', allowed: true }],
      }],
    })
    apiMock.createFolder.mockResolvedValue({ id: 'folder-new', name: 'Imported APIs', parent_id: null })
  })

  afterEach(() => {
    act(() => root.unmount())
    container.remove()
  })

  async function render(props: Partial<Parameters<typeof ImportDestinationFolder>[0]> = {}) {
    await act(async () => {
      root.render(
        <ImportDestinationFolder projectId="project-1" value="" onChange={vi.fn()} {...props} />,
      )
    })
    await flushPromises()
  }

  it('offers the project root plus every folder, with the root first', async () => {
    apiMock.listFolders.mockResolvedValue({
      folders: [
        { id: 'folder-b', name: 'Beta', parent_id: null },
        { id: 'folder-a', name: 'Alpha', parent_id: null },
      ],
      count: 2,
    })

    await render()

    expect(apiMock.listFolders).toHaveBeenCalledWith('project-1', 'scripts')
    const options = Array.from(container.querySelectorAll('option')).map(option => option.textContent)
    // Root must lead: saving to the project root is the established default, and
    // every format except Bruno has always saved without a folder.
    expect(options[0]).toBe('Project root')
    expect(options).toContain('Alpha')
    expect(options).toContain('Beta')
    expect(container.querySelector('option')?.getAttribute('value')).toBe('')
  })

  it('reports the chosen folder id to its owner', async () => {
    const onChange = vi.fn()
    apiMock.listFolders.mockResolvedValue({
      folders: [{ id: 'folder-a', name: 'Alpha', parent_id: null }],
      count: 1,
    })

    await render({ onChange })

    const select = container.querySelector('select') as HTMLSelectElement | null
    expect(select).toBeTruthy()
    if (!select) return
    await act(async () => {
      select.value = 'folder-a'
      select.dispatchEvent(new Event('change', { bubbles: true }))
    })
    expect(onChange).toHaveBeenCalledWith('folder-a')
  })

  it('keeps Project root visible when the project has no folders', async () => {
    apiMock.listFolders.mockResolvedValue({ folders: [], count: 0 })

    await render()

    const select = container.querySelector<HTMLSelectElement>('select')
    expect(select).not.toBeNull()
    expect(select?.value).toBe('')
    expect(select?.querySelectorAll('option')).toHaveLength(1)
    expect(container.textContent).toContain('No script folders exist in this project yet. Imports will save to Project root, or create a folder below.')
    expect(container.textContent).not.toContain('Project Administrator creates one')
  })

  it('does not offer a folder to someone who cannot create one', async () => {
    apiMock.listFolders.mockResolvedValue({ folders: [], count: 0 })
    apiMock.getAuthEffectiveAccess.mockResolvedValue({
      resources: [{
        id: 'projects_and_folders',
        operations: [{ id: 'manage_projects', allowed: false }],
      }],
    })

    await render()

    expect(container.textContent).toContain('No script folders exist in this project. Imports will save to Project root.')
    expect(container.textContent).not.toContain('create a folder below')
  })

  it('shows a disabled Project root choice before a project is selected, without calling the API', async () => {
    await render({ projectId: undefined })

    expect(apiMock.listFolders).not.toHaveBeenCalled()
    expect(apiMock.getAuthEffectiveAccess).not.toHaveBeenCalled()
    const select = container.querySelector<HTMLSelectElement>('select')
    expect(select?.disabled).toBe(true)
    expect(select?.options[0].textContent).toBe('Project root')
    expect(container.textContent).toContain('Select a project to choose a script destination')
  })

  it('surfaces a load failure instead of silently offering only the root', async () => {
    // Failing quietly would leave the user believing the project has no folders
    // and their script saved where they intended.
    apiMock.listFolders.mockRejectedValue(new Error('folder service unavailable'))

    function Harness() {
      const [folderId, setFolderId] = useState('stale-folder')
      return (
        <>
          <span data-testid="controlled-folder">{folderId || 'project-root'}</span>
          <ImportDestinationFolder projectId="project-1" value={folderId} onChange={setFolderId} />
        </>
      )
    }
    await act(async () => {
      root.render(<Harness />)
    })
    await flushPromises()

    const alert = container.querySelector('[role="alert"]')
    expect(alert, 'a load failure must be announced, not swallowed').not.toBeNull()
    expect(alert?.textContent).toContain('folder service unavailable')
    expect(container.querySelector<HTMLSelectElement>('select')?.value).toBe('')
    expect(container.querySelector('[data-testid="controlled-folder"]')?.textContent).toBe('project-root')
  })

  it('locks every destination mutation while an import operation is in progress', async () => {
    apiMock.listFolders.mockResolvedValue({
      folders: [{ id: 'folder-a', name: 'Alpha', parent_id: null }],
      count: 1,
    })

    await render({ disabled: true })

    expect(container.querySelector<HTMLSelectElement>('select')?.disabled).toBe(true)
    expect(container.querySelector<HTMLInputElement>('.import-destination-folder-create input')?.disabled).toBe(true)
    expect(Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Create and select folder'))?.disabled).toBe(true)
    expect(container.textContent).toContain('destination is locked until it finishes')
  })

  it('clears a folder selection that belongs to the previous project', async () => {
    apiMock.listFolders
      .mockResolvedValueOnce({ folders: [{ id: 'folder-a', name: 'Alpha', parent_id: null }], count: 1 })
      .mockResolvedValueOnce({ folders: [{ id: 'folder-b', name: 'Beta', parent_id: null }], count: 1 })

    function Harness({ projectId }: { projectId: string }) {
      const [folderId, setFolderId] = useState('folder-a')
      return <ImportDestinationFolder projectId={projectId} value={folderId} onChange={setFolderId} />
    }

    await act(async () => {
      root.render(<Harness projectId="project-1" />)
      await Promise.resolve()
      await Promise.resolve()
    })
    expect(container.querySelector<HTMLSelectElement>('select')?.value).toBe('folder-a')

    await act(async () => {
      root.render(<Harness projectId="project-2" />)
      await Promise.resolve()
      await Promise.resolve()
    })
    expect(apiMock.listFolders).toHaveBeenLastCalledWith('project-2', 'scripts')
    expect(container.querySelector<HTMLSelectElement>('select')?.value).toBe('')
    expect(container.textContent).toContain('Project root')
  })

  it('creates and selects a script folder only when effective access allows it', async () => {
    const onChange = vi.fn()
    apiMock.listFolders.mockResolvedValue({ folders: [], count: 0 })
    await render({ onChange })

    const input = container.querySelector<HTMLInputElement>('.import-destination-folder-create input')!
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')?.set
      setter?.call(input, 'Imported APIs')
      input.dispatchEvent(new Event('input', { bubbles: true }))
    })
    const create = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Create and select folder'))!
    await act(async () => {
      create.click()
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(apiMock.createFolder).toHaveBeenCalledWith('project-1', {
      name: 'Imported APIs',
      category: 'scripts',
    })
    expect(onChange).toHaveBeenCalledWith('folder-new')
    expect(container.textContent).toContain('Created and selected script folder “Imported APIs”')
  })

  it('fences script saving and destination changes for the lifetime of a deferred folder create', async () => {
    const pendingCreate = deferred<{ id: string; name: string; parent_id: null }>()
    apiMock.listFolders.mockResolvedValue({
      folders: [{ id: 'folder-a', name: 'Alpha', parent_id: null }],
      count: 1,
    })
    apiMock.createFolder.mockReturnValueOnce(pendingCreate.promise)

    function Harness() {
      const [folderId, setFolderId] = useState('folder-a')
      const [destinationBusy, setDestinationBusy] = useState(false)
      return (
        <>
          <ImportDestinationFolder
            projectId="project-1"
            value={folderId}
            onChange={setFolderId}
            onOperationBusyChange={setDestinationBusy}
          />
          <button type="button" disabled={destinationBusy}>Save imported scripts</button>
        </>
      )
    }
    await act(async () => {
      root.render(<Harness />)
    })
    await flushPromises()

    const input = container.querySelector<HTMLInputElement>('.import-destination-folder-create input')!
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')?.set
      setter?.call(input, 'Deferred APIs')
      input.dispatchEvent(new Event('input', { bubbles: true }))
    })
    await act(async () => {
      Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
        .find(button => button.textContent?.includes('Create and select folder'))?.click()
      await Promise.resolve()
    })

    expect(container.querySelector<HTMLSelectElement>('select')?.disabled).toBe(true)
    expect(Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent === 'Save imported scripts')?.disabled).toBe(true)
    expect(container.textContent).toContain('Script import actions and destination changes are locked')

    await act(async () => {
      pendingCreate.resolve({ id: 'folder-deferred', name: 'Deferred APIs', parent_id: null })
      await pendingCreate.promise
    })
    await flushPromises()

    expect(container.querySelector<HTMLSelectElement>('select')?.value).toBe('folder-deferred')
    expect(container.querySelector<HTMLSelectElement>('select')?.disabled).toBe(false)
    expect(Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent === 'Save imported scripts')?.disabled).toBe(false)
  })

  it('releases a stale folder-create fence when the project changes', async () => {
    const pendingCreate = deferred<{ id: string; name: string; parent_id: null }>()
    apiMock.listFolders
      .mockResolvedValueOnce({ folders: [], count: 0 })
      .mockResolvedValueOnce({ folders: [{ id: 'folder-b', name: 'Beta', parent_id: null }], count: 1 })
      .mockResolvedValueOnce({ folders: [{ id: 'folder-current-a', name: 'Current Alpha', parent_id: null }], count: 1 })
    apiMock.createFolder.mockReturnValueOnce(pendingCreate.promise)

    function Harness({ projectId }: { projectId: string }) {
      const [folderId, setFolderId] = useState('')
      return <ImportDestinationFolder projectId={projectId} value={folderId} onChange={setFolderId} />
    }
    await act(async () => {
      root.render(<Harness projectId="project-1" />)
    })
    await flushPromises()
    const input = container.querySelector<HTMLInputElement>('.import-destination-folder-create input')!
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')?.set
      setter?.call(input, 'Old project folder')
      input.dispatchEvent(new Event('input', { bubbles: true }))
    })
    await act(async () => {
      Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
        .find(button => button.textContent?.includes('Create and select folder'))?.click()
      await Promise.resolve()
    })
    expect(container.textContent).toContain('Creating folder…')

    await act(async () => {
      root.render(<Harness projectId="project-2" />)
    })
    await flushPromises()

    expect(container.textContent).not.toContain('Creating folder…')
    expect(container.querySelector<HTMLSelectElement>('select')?.disabled).toBe(false)
    expect(container.textContent).toContain('Beta')

    // Returning to the original project must not make the earlier visit's
    // pending request current again.
    await act(async () => {
      root.render(<Harness projectId="project-1" />)
    })
    await flushPromises()
    expect(container.textContent).toContain('Current Alpha')

    await act(async () => {
      pendingCreate.resolve({ id: 'stale-folder', name: 'Old project folder', parent_id: null })
      await pendingCreate.promise
    })
    await flushPromises()
    expect(container.textContent).not.toContain('Old project folder')
    expect(container.querySelector<HTMLSelectElement>('select')?.value).toBe('')
  })

  it('translates a folder-create 403 into administrator guidance and removes the create affordance', async () => {
    apiMock.listFolders.mockResolvedValue({ folders: [], count: 0 })
    apiMock.createFolder.mockRejectedValueOnce(new ApiError('Current session is not allowed to perform this action', 403))
    await render()

    const input = container.querySelector<HTMLInputElement>('.import-destination-folder-create input')!
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')?.set
      setter?.call(input, 'Imported APIs')
      input.dispatchEvent(new Event('input', { bubbles: true }))
    })
    await act(async () => {
      Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
        .find(button => button.textContent?.includes('Create and select folder'))?.click()
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(container.textContent).not.toContain('Current session is not allowed')
    expect(container.textContent).toContain('ask a Project Administrator to create one')
    expect(container.querySelector('.import-destination-folder-create')).toBeNull()
  })

  it('never offers folder creation when effective access denies manage_projects', async () => {
    apiMock.listFolders.mockResolvedValue({ folders: [], count: 0 })
    apiMock.getAuthEffectiveAccess.mockResolvedValueOnce({
      resources: [{ id: 'projects_and_folders', operations: [{ id: 'manage_projects', allowed: false }] }],
    })

    await render()

    expect(container.querySelector('.import-destination-folder-create')).toBeNull()
    expect(container.textContent).toContain('Your current role can import scripts but cannot create project folders')
  })
})
