// @vitest-environment jsdom

import { act, type ReactElement } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi, type MockInstance } from 'vitest'

import LibraryAssetSourcePanel from './LibraryAssetSourcePanel'
import type { LibraryCatalogItem } from '../types/library'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('LibraryAssetSourcePanel', () => {
  let container: HTMLDivElement
  let root: Root
  let fetchMock: MockInstance<(input: RequestInfo | URL, init?: RequestInit) => Promise<Response>>
  let remoteOverride: Record<string, unknown>
  let localCatalogItems: LibraryCatalogItem[]
  let remoteCatalogItems: LibraryCatalogItem[]

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    remoteOverride = {}
    localCatalogItems = [{
      kind: 'script',
      id: 'local-script',
      name: 'Local Checkout',
      project_id: 'project-1',
      protocol: 'http',
      content_hash: 'localhash123456',
      updated_at: '2026-05-01T00:00:00Z',
    }]
    remoteCatalogItems = [{
      kind: 'script',
      id: 'host-script',
      name: 'Host Checkout',
      project_id: 'project-1',
      protocol: 'http',
      content_hash: 'hosthash123456',
      updated_at: '2026-05-02T00:00:00Z',
      sync_status: 'host only',
    }]
    fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
      const url = String(input)
      const method = init?.method || 'GET'
      if (url.includes('/api/v1/library/remotes') && method === 'GET' && !url.includes('/catalog')) {
        return jsonResponse({
          remotes: [{
            id: 'remote-1',
            name: 'Team AKS',
            base_url: 'https://apexload.example.test',
            default_project_id: 'project-1',
            auth_method: 'service_account',
            credential_configured: true,
            status: 'healthy',
            ...remoteOverride,
          }],
          count: 1,
        })
      }
      if (url.includes('/api/v1/library/catalog')) {
        return jsonResponse({
          items: localCatalogItems,
          count: localCatalogItems.length,
        })
      }
      if (url.includes('/api/v1/library/remotes/remote-1/catalog')) {
        return jsonResponse({
          remote: { id: 'remote-1', name: 'Team AKS', base_url: 'https://apexload.example.test' },
          catalog: {
            items: remoteCatalogItems,
            count: remoteCatalogItems.length,
          },
        })
      }
      if (url.includes('/api/v1/library/remotes/remote-1/pull') && method === 'POST') {
        return jsonResponse({
          import: {
            items: [{ kind: 'script', local_item_id: 'local-copy', action: 'created' }],
          },
        })
      }
      if (url.includes('/api/v1/library/remotes/remote-1/push') && method === 'POST') {
        return jsonResponse({
          import: {
            items: [{ kind: 'script', local_item_id: 'local-script', action: 'created' }],
          },
        })
      }
      return jsonResponse({})
    })
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    fetchMock.mockRestore()
  })

  it('shows local and hosted catalog rows and routes transfers through the local controller', async () => {
    const onRefreshLocal = vi.fn()
    await act(async () => {
      root.render(<LibraryAssetSourcePanel kind="script" projectId="project-1" onRefreshLocal={onRefreshLocal} />)
    })
    await flushPromises()
    await flushPromises()

    expect(container.textContent).toContain('Team AKS')
    expect(container.textContent).toContain('Local Checkout')
    expect(container.textContent).toContain('Host Checkout')
    expect(container.textContent).toContain('host only')
    expect(styledLibrarySelects()).toHaveLength(2)
    expect(styledLibrarySelects().map(select => select.parentElement?.querySelector('span')?.textContent)).toEqual(['Remote', 'Source'])

    const pullButton = findButton('Pull')
    expect(pullButton).toBeDefined()
    await act(async () => {
      pullButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(fetchMock.mock.calls.some(([input]) => String(input).includes('/api/v1/library/remotes/remote-1/pull'))).toBe(true)
    expect(container.textContent).toContain('Pulled 1 item.')

    const pushButton = findButton('Push')
    expect(pushButton).toBeDefined()
    await act(async () => {
      pushButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(fetchMock.mock.calls.some(([input]) => String(input).includes('/api/v1/library/remotes/remote-1/push'))).toBe(true)
    expect(container.textContent).toContain('Pushed 1 item.')
    expect(onRefreshLocal).toHaveBeenCalled()
  })

  it('selects individual catalog rows and shows row-specific source details', async () => {
    await renderPanel(root, <LibraryAssetSourcePanel kind="script" projectId="project-1" />)

    expect(container.querySelector('.library-source-detail-row')).toBeNull()

    const localRow = rowContaining('Local Checkout')
    await click(localRow)

    expect(localRow.classList.contains('selected')).toBe(true)
    expect(localRow.getAttribute('aria-expanded')).toBe('true')
    expect(container.textContent).toContain('Local ID')
    expect(container.textContent).toContain('local-script')

    await click(localRow)
    expect(container.querySelector('.library-source-detail-row')).toBeNull()

    const remoteRow = rowContaining('Host Checkout')
    await keyDown(remoteRow, 'Enter')

    expect(remoteRow.classList.contains('selected')).toBe(true)
    expect(remoteRow.getAttribute('aria-expanded')).toBe('true')
    expect(container.textContent).toContain('Remote ID')
    expect(container.textContent).toContain('host-script')
  })

  it('lets pages route Remote Workspace status controls to configuration', async () => {
    const onConfigureRemotes = vi.fn()
    await renderPanel(root, <LibraryAssetSourcePanel kind="script" projectId="project-1" onConfigureRemotes={onConfigureRemotes} />)

    await click(findButton('healthy')!)

    expect(onConfigureRemotes).toHaveBeenCalledTimes(1)
  })

  it('sorts result source rows, filters by release, and exposes view/delete controls', async () => {
    localCatalogItems = [
      {
        kind: 'result',
        id: 'run-z',
        name: 'Zebra Run',
        project_id: 'project-1',
        release_id: 'rel-30',
        release_name: 'PI30',
        status: 'FAILED',
        content_hash: 'zebrarun123456',
        execution_run_date: '2026-05-01T13:00:00Z',
        updated_at: '2026-05-03T00:00:00Z',
        has_archive: true,
      },
      {
        kind: 'result',
        id: 'run-a',
        name: 'Alpha Run',
        project_id: 'project-1',
        release_id: 'rel-29',
        release_name: 'PI29',
        status: 'COMPLETE',
        content_hash: 'alpharun123456',
        execution_run_date: '2026-05-01T12:00:00Z',
        updated_at: '2026-05-02T00:00:00Z',
        has_archive: true,
      },
    ]
    remoteCatalogItems = []
    const onViewLocalItem = vi.fn()
    const onDeleteLocalItem = vi.fn()
    await renderPanel(root, (
      <LibraryAssetSourcePanel
        kind="result"
        projectId="project-1"
        onViewLocalItem={onViewLocalItem}
        onDeleteLocalItem={onDeleteLocalItem}
      />
    ))

    expect(container.textContent).toContain('Release')
    expect(container.textContent).toContain('Execution Run Date')
    expect(releaseFilter().textContent).toContain('PI29')
    expect(releaseFilter().textContent).toContain('PI30')
    expect(rowNamed('Alpha Run').textContent).toContain('COMPLETE')
    expect(rowNamed('Alpha Run').textContent).toContain('5/1/2026')
    expect(rowNamed('Alpha Run').querySelectorAll('.script-table-meta .script-health-token')).toHaveLength(0)

    const nameSortButton = headerNamed('Name')
    expect(nameSortButton.closest('th')?.getAttribute('aria-sort')).toBe('none')
    expect(nameSortButton.tabIndex).toBe(0)
    await click(nameSortButton)
    expect(visibleLibraryRowNames()).toEqual(['Alpha Run', 'Zebra Run'])
    expect(nameSortButton.closest('th')?.getAttribute('aria-sort')).toBe('ascending')
    expect(headerNamed('Execution Run Date').closest('th')?.getAttribute('aria-sort')).toBe('none')

    await changeSelect(releaseFilter(), 'rel-29')
    expect(visibleLibraryRowNames()).toEqual(['Alpha Run'])
    expect(container.textContent).not.toContain('Zebra Run')

    await click(findButton('View')!)
    expect(onViewLocalItem).toHaveBeenCalledWith('run-a')

    await click(findButton('Delete')!)
    expect(onDeleteLocalItem).toHaveBeenCalledWith('run-a')

    await click(rowNamed('Alpha Run'))
    expect(container.querySelector('.library-source-detail-row')).toBeNull()
  })

  it('disables transfers when the selected remote has no configured credentials', async () => {
    remoteOverride = { credential_configured: false }
    await renderPanel(root, <LibraryAssetSourcePanel kind="script" projectId="project-1" />)

    expect(container.textContent).toContain('Credentials Missing')
    expect(container.textContent).toContain('Configure remote credentials in Settings first.')
    expect(findButton('Pull')?.disabled).toBe(true)
    expect(findButton('Push')?.disabled).toBe(true)
    expect(transferCalls(fetchMock)).toHaveLength(0)
  })

  it('requires an explicit project mapping when the remote has no default project', async () => {
    remoteOverride = { default_project_id: '' }
    await renderPanel(root, <LibraryAssetSourcePanel kind="script" />)

    expect(container.textContent).toContain('Set a project mapping before transferring assets.')
    expect(findButton('Pull')?.disabled).toBe(true)
    expect(findButton('Push')?.disabled).toBe(true)
    expect(transferCalls(fetchMock)).toHaveLength(0)
  })

  it('blocks transfer actions for conflicts while keeping compare available', async () => {
    remoteCatalogItems = [{
      kind: 'script',
      id: 'host-script',
      name: 'Host Checkout',
      project_id: 'project-1',
      protocol: 'http',
      content_hash: 'hosthash123456',
      updated_at: '2026-05-02T00:00:00Z',
      sync_status: 'conflict',
      local_item_id: 'local-script',
      remote_item_id: 'host-script',
    }]
    await renderPanel(root, <LibraryAssetSourcePanel kind="script" projectId="project-1" />)

    expect(container.textContent).toContain('conflict')
    expect(findButton('Pull')?.disabled).toBe(true)
    expect(findButton('Push')?.disabled).toBe(true)
    expect(findButtons('Compare')).toHaveLength(2)
    expect(findButtons('Compare').every(button => !button.disabled)).toBe(true)
    expect(transferCalls(fetchMock)).toHaveLength(0)
  })
})

function jsonResponse(body: unknown) {
  return Promise.resolve(new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  }))
}

function findButton(label: string) {
  return Array.from(document.querySelectorAll('button')).find(button => button.textContent?.trim() === label) as HTMLButtonElement | undefined
}

function findButtons(label: string) {
  return Array.from(document.querySelectorAll('button')).filter(button => button.textContent?.trim() === label) as HTMLButtonElement[]
}

function styledLibrarySelects() {
  return Array.from(document.querySelectorAll('select.library-source-select')) as HTMLSelectElement[]
}

function rowContaining(text: string) {
  const row = Array.from(document.querySelectorAll('tbody tr.clickable-row')).find(row => row.textContent?.includes(text))
  expect(row).toBeTruthy()
  return row as HTMLTableRowElement
}

function rowNamed(text: string) {
  const row = Array.from(document.querySelectorAll('.library-source-table tbody tr')).find(row => row.querySelector('.cell-name > strong')?.textContent?.trim() === text)
  expect(row).toBeTruthy()
  return row as HTMLTableRowElement
}

function headerNamed(text: string) {
  const button = Array.from(document.querySelectorAll<HTMLButtonElement>('th.sortable > button')).find(candidate => candidate.textContent?.includes(text))
  expect(button).toBeTruthy()
  return button as HTMLButtonElement
}

function releaseFilter() {
  const select = Array.from(document.querySelectorAll('select.library-release-filter'))[0]
  expect(select).toBeTruthy()
  return select as HTMLSelectElement
}

function visibleLibraryRowNames() {
  return Array.from(document.querySelectorAll('.library-source-table tbody tr .cell-name > strong'))
    .map(element => element.textContent?.trim() || '')
}

async function click(element: HTMLElement) {
  await act(async () => {
    element.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true }))
  })
}

async function changeSelect(select: HTMLSelectElement, value: string) {
  await act(async () => {
    select.value = value
    select.dispatchEvent(new Event('change', { bubbles: true }))
  })
}

async function keyDown(element: HTMLElement, key: string) {
  await act(async () => {
    element.dispatchEvent(new KeyboardEvent('keydown', { key, bubbles: true, cancelable: true }))
  })
}

async function flushPromises() {
  await act(async () => {
    await Promise.resolve()
  })
}

async function renderPanel(root: Root, element: ReactElement) {
  await act(async () => {
    root.render(element)
  })
  await flushPromises()
  await flushPromises()
}

function transferCalls(fetchMock: MockInstance<(input: RequestInfo | URL, init?: RequestInit) => Promise<Response>>) {
  return fetchMock.mock.calls.filter(([input]) => {
    const url = String(input)
    return url.includes('/api/v1/library/remotes/remote-1/pull') || url.includes('/api/v1/library/remotes/remote-1/push')
  })
}
