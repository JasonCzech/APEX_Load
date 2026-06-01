// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi, type MockInstance } from 'vitest'

import LibraryFederationPanel from './LibraryFederationPanel'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('LibraryFederationPanel', () => {
  let container: HTMLDivElement
  let root: Root
  let fetchMock: MockInstance<(input: RequestInfo | URL, init?: RequestInit) => Promise<Response>>

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
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
            status: 'healthy',
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
    await act(async () => {
      root.render(<LibraryFederationPanel projectId="project-1" />)
    })
    await flushPromises()
    await flushPromises()

    expect(container.textContent).toContain('Team AKS')
    expect(container.textContent).toContain('Local Checkout')
    expect(container.textContent).toContain('Host Checkout')
    expect(container.textContent).toContain('host only')

    const pullButton = findButton('Pull')
    expect(pullButton).toBeDefined()
    if (!pullButton) return
    await act(async () => {
      pullButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(fetchMock.mock.calls.some(([input]) => String(input).includes('/api/v1/library/remotes/remote-1/pull'))).toBe(true)
    expect(container.textContent).toContain('Pulled 1 item.')

    const pushButton = findButton('Push')
    expect(pushButton).toBeDefined()
    if (!pushButton) return
    await act(async () => {
      pushButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(fetchMock.mock.calls.some(([input]) => String(input).includes('/api/v1/library/remotes/remote-1/push'))).toBe(true)
    expect(container.textContent).toContain('Pushed 1 item.')
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
