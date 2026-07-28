// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import ImportDestinationFolder from './ImportDestinationFolder'

const apiMock = {
  listFolders: vi.fn(),
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

describe('ImportDestinationFolder', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    vi.clearAllMocks()
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
    expect(select).not.toBeNull()
    if (!select) return
    await act(async () => {
      select.value = 'folder-a'
      select.dispatchEvent(new Event('change', { bubbles: true }))
    })
    expect(onChange).toHaveBeenCalledWith('folder-a')
  })

  it('renders nothing when the project has no folders', async () => {
    // An empty dropdown reads as a broken control rather than as an absent
    // feature, and most projects define no script folders at all.
    apiMock.listFolders.mockResolvedValue({ folders: [], count: 0 })

    await render()

    expect(container.querySelector('select')).toBeNull()
  })

  it('renders nothing before a project is selected, without calling the API', async () => {
    await render({ projectId: undefined })

    expect(apiMock.listFolders).not.toHaveBeenCalled()
    expect(container.querySelector('select')).toBeNull()
  })

  it('surfaces a load failure instead of silently offering only the root', async () => {
    // Failing quietly would leave the user believing the project has no folders
    // and their script saved where they intended.
    apiMock.listFolders.mockRejectedValue(new Error('folder service unavailable'))

    await render()

    const alert = container.querySelector('[role="alert"]')
    expect(alert, 'a load failure must be announced, not swallowed').not.toBeNull()
    expect(alert?.textContent).toContain('folder service unavailable')
  })
})
