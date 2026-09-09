// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { LibraryCatalogItem } from '../types/library'
import { useLibraryFederation } from './useLibraryFederation'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const libraryApiMock = {
  listRemotes: vi.fn(),
  localCatalog: vi.fn(),
  remoteCatalog: vi.fn(),
  pullRemoteItem: vi.fn(),
  pushLocalItem: vi.fn(),
  compareItems: vi.fn(),
}

vi.mock('../hooks/api/useLibraryApi', () => ({
  useLibraryApi: () => libraryApiMock,
}))

function FederationHarness({ projectId, onRefreshLocal }: { projectId?: string; onRefreshLocal?: () => Promise<void> }) {
  const federation = useLibraryFederation({ kind: 'script', projectId, onRefreshLocal, requireProjectScope: true })
  return (
    <div>
      <button type="button" onClick={() => void federation.refreshCatalogs()}>Refresh libraries</button>
      <span data-testid="mapping">{federation.projectMapping}</span>
      <span data-testid="effective-project">{federation.effectiveProjectID}</span>
      <span data-testid="local-items">{federation.localItems.map(item => item.name).join(',')}</span>
      <span data-testid="remote-items">{federation.remoteItems.map(item => item.name).join(',')}</span>
      <span data-testid="action-error">{federation.actionError}</span>
      <span data-testid="loading">{String(federation.loading)}</span>
      <button type="button" onClick={() => federation.setProjectMapping('custom-project')}>Use custom mapping</button>
      <button
        type="button"
        onClick={() => void federation.pushLocalItem({ kind: 'script', id: 'script-1', name: 'Checkout' })}
      >Push item</button>
    </div>
  )
}

describe('useLibraryFederation', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    libraryApiMock.listRemotes.mockResolvedValue({ remotes: [], count: 0 })
    libraryApiMock.localCatalog.mockResolvedValue({ items: [], count: 0 })
    libraryApiMock.remoteCatalog.mockResolvedValue({ catalog: { items: [], count: 0 } })
    libraryApiMock.pullRemoteItem.mockResolvedValue({ import: { items: [], conflicts: [] } })
    libraryApiMock.pushLocalItem.mockResolvedValue({ import: { items: [], conflicts: [] } })
    libraryApiMock.compareItems.mockResolvedValue({ kind: 'script', status: 'synced' })
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    vi.clearAllMocks()
  })

  it('refreshes the visible local library with the federation catalog only on explicit refresh', async () => {
    const refreshLocal = vi.fn().mockResolvedValue(undefined)
    await act(async () => root.render(<FederationHarness projectId="project-1" onRefreshLocal={refreshLocal} />))
    await flushPromises()
    expect(refreshLocal).not.toHaveBeenCalled()
    const initialCalls = libraryApiMock.localCatalog.mock.calls.length
    await act(async () => { Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Refresh libraries')!.click() })
    await flushPromises()
    expect(refreshLocal).toHaveBeenCalledTimes(1)
    expect(libraryApiMock.localCatalog.mock.calls.length).toBe(initialCalls + 1)
  })

  it('waits for a project mapping before loading a catalog', async () => {
    libraryApiMock.listRemotes.mockResolvedValue({ remotes: [remoteFixture()], count: 1 })
    await act(async () => {
      root.render(<FederationHarness />)
    })
    await flushPromises()
    await flushPromises()

    expect(libraryApiMock.localCatalog).not.toHaveBeenCalled()
    expect(libraryApiMock.remoteCatalog).not.toHaveBeenCalled()

    await act(async () => {
      root.render(<FederationHarness projectId="project-1" />)
    })
    await flushPromises()

    expect(libraryApiMock.localCatalog).toHaveBeenCalledWith('script', 'project-1', expect.objectContaining({ limit: 100, offset: 0 }))
    expect(libraryApiMock.remoteCatalog).toHaveBeenCalledWith('remote-1', 'script', 'project-1', expect.objectContaining({ limit: 100, offset: 0 }))
    expect(container.querySelector('[data-testid="mapping"]')?.textContent).toBe('project-1')
  })

  it('updates the mapping and ignores an older project catalog response', async () => {
    const projectACatalog = deferred<{ items: LibraryCatalogItem[]; count: number }>()
    let projectASignal: AbortSignal | undefined
    libraryApiMock.localCatalog.mockImplementation((_kind: string, projectId: string, page?: { signal?: AbortSignal }) => {
      if (projectId === 'project-a') {
        projectASignal = page?.signal
        return projectACatalog.promise
      }
      return Promise.resolve({
        items: [{ kind: 'script', id: 'script-b', name: 'Project B script' }],
        count: 1,
      })
    })

    await act(async () => {
      root.render(<FederationHarness projectId="project-a" />)
    })
    await flushPromises()

    expect(libraryApiMock.localCatalog).toHaveBeenCalledWith('script', 'project-a', expect.objectContaining({ limit: 100, offset: 0 }))

    await act(async () => {
      root.render(<FederationHarness projectId="project-b" />)
    })
    await flushPromises()

    expect(projectASignal?.aborted).toBe(true)
    expect(container.querySelector('[data-testid="mapping"]')?.textContent).toBe('project-b')
    expect(container.querySelector('[data-testid="local-items"]')?.textContent).toBe('Project B script')

    await act(async () => {
      projectACatalog.resolve({
        items: [{ kind: 'script', id: 'script-a', name: 'Project A script' }],
        count: 1,
      })
      await projectACatalog.promise
    })
    await flushPromises()

    expect(container.querySelector('[data-testid="local-items"]')?.textContent).toBe('Project B script')
    expect(container.textContent).not.toContain('Project A script')
  })

  it('loads every local catalog page and deduplicates overlapping rows', async () => {
    libraryApiMock.localCatalog.mockImplementation(async (_kind: string, _projectId: string, page?: { offset?: number }) => {
      if (page?.offset === 0) {
        return {
          items: [
            { kind: 'script', id: 'script-1', name: 'First script' },
            { kind: 'script', id: 'script-overlap', name: 'Overlap (older)' },
          ],
          count: 2,
          total: 4,
          limit: 100,
          offset: 0,
          has_more: true,
        }
      }
      return {
        items: [
          { kind: 'script', id: 'script-overlap', name: 'Overlap (newer)' },
          { kind: 'script', id: 'script-late', name: 'Late script' },
        ],
        count: 2,
        total: 4,
        limit: 100,
        offset: 2,
        has_more: false,
      }
    })

    await act(async () => {
      root.render(<FederationHarness projectId="project-1" />)
    })
    await flushPromises()
    await flushPromises()

    expect(container.querySelector('[data-testid="local-items"]')?.textContent).toBe('First script,Overlap (newer),Late script')
    expect(libraryApiMock.localCatalog).toHaveBeenCalledWith('script', 'project-1', expect.objectContaining({ offset: 2, limit: 100 }))
  })

  it('does not publish a partial catalog when a later page fails', async () => {
    libraryApiMock.localCatalog.mockImplementation(async (_kind: string, _projectId: string, page?: { offset?: number }) => {
      if (page?.offset === 0) {
        return {
          items: [{ kind: 'script', id: 'script-1', name: 'Partial script' }],
          count: 1,
          total: 2,
          offset: 0,
          has_more: true,
        }
      }
      throw new Error('second catalog page failed')
    })

    await act(async () => {
      root.render(<FederationHarness projectId="project-1" />)
    })
    await flushPromises()
    await flushPromises()

    expect(container.querySelector('[data-testid="local-items"]')?.textContent).toBe('')
    expect(container.querySelector('[data-testid="action-error"]')?.textContent).toContain('second catalog page failed')
    expect(container.querySelector('[data-testid="loading"]')?.textContent).toBe('false')
  })

  it('resets a manual mapping before transferring from a newly selected project', async () => {
    libraryApiMock.listRemotes.mockResolvedValue({
      remotes: [remoteFixture()],
      count: 1,
    })

    await act(async () => {
      root.render(<FederationHarness projectId="project-a" />)
    })
    await flushPromises()
    await flushPromises()

    await clickButton('Use custom mapping')
    expect(container.querySelector('[data-testid="mapping"]')?.textContent).toBe('custom-project')

    await act(async () => {
      root.render(<FederationHarness projectId="project-b" />)
    })
    await flushPromises()
    expect(container.querySelector('[data-testid="mapping"]')?.textContent).toBe('project-b')

    await clickButton('Push item')
    await flushPromises()

    expect(libraryApiMock.pushLocalItem).toHaveBeenCalledWith('remote-1', expect.objectContaining({
      kind: 'script',
      id: 'script-1',
      project_id: 'project-b',
    }))
  })

  async function clickButton(label: string) {
    const button = Array.from(container.querySelectorAll('button')).find(item => item.textContent === label)
    expect(button).toBeDefined()
    await act(async () => {
      button?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
  }
})

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise
    reject = rejectPromise
  })
  return { promise, resolve, reject }
}

function remoteFixture() {
  return {
    id: 'remote-1',
    name: 'Team Remote',
    base_url: 'https://remote.example.test',
    default_project_id: 'remote-default',
    project_mappings: {
      'project-1': 'remote-project-1',
      'project-a': 'remote-project-a',
      'project-b': 'remote-project-b',
      'custom-project': 'remote-custom-project',
    },
    auth_method: 'none',
    credential_configured: false,
    status: 'healthy',
  }
}

async function flushPromises() {
  await act(async () => {
    await Promise.resolve()
    await Promise.resolve()
  })
}
