// @vitest-environment jsdom

import { act, type ReactElement } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi, type MockInstance } from 'vitest'

import LibraryAssetSourcePanel from './LibraryAssetSourcePanel'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('LibraryAssetSourcePanel', () => {
  let container: HTMLDivElement
  let root: Root
  let fetchMock: MockInstance<(input: RequestInfo | URL, init?: RequestInit) => Promise<Response>>
  let remoteOverride: Record<string, unknown>

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    remoteOverride = {}
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
          items: [{
            kind: 'script',
            id: 'local-script',
            name: 'Local Checkout',
            project_id: 'project-1',
            protocol: 'http',
            content_hash: 'localhash123456',
            updated_at: '2026-05-01T00:00:00Z',
          }],
          count: 1,
        })
      }
      if (url.includes('/api/v1/library/remotes/remote-1/catalog')) {
        return jsonResponse({
          remote: { id: 'remote-1', name: 'Team AKS', base_url: 'https://apexload.example.test' },
          catalog: {
            items: [{
              kind: 'script',
              id: 'host-script',
              name: 'Host Checkout',
              project_id: 'project-1',
              protocol: 'http',
              content_hash: 'hosthash123456',
              updated_at: '2026-05-02T00:00:00Z',
              sync_status: 'host only',
            }],
            count: 1,
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

  it('disables transfers when the selected remote has no configured credentials', async () => {
    remoteOverride = { credential_configured: false }
    await renderPanel(root, <LibraryAssetSourcePanel kind="script" projectId="project-1" />)

    expect(container.textContent).toContain('credentials missing')
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
