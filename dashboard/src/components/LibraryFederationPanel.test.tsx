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
  let confirmMock: MockInstance<(message?: string) => boolean>

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    confirmMock = vi.spyOn(globalThis, 'confirm').mockReturnValue(true)
    fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
      const url = String(input)
      const method = init?.method || 'GET'
      if (url.includes('/api/v1/library/remotes') && method === 'GET') {
        return jsonResponse({
          remotes: [remotePayload()],
          count: 1,
        })
      }
      if (url.includes('/api/v1/library/remotes/remote-1/probe') && method === 'POST') {
        return jsonResponse({ status: 'healthy' })
      }
      if (url.includes('/api/v1/library/remotes/remote-1') && method === 'PUT') {
        return jsonResponse({ ...remotePayload(), name: 'Team AKS' })
      }
      if (url.includes('/api/v1/library/remotes/remote-1') && method === 'DELETE') {
        return jsonResponse({ deleted: 'remote-1' })
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
    confirmMock.mockRestore()
  })

  it('manages remote connection state without receiving browser-visible API keys', async () => {
    await act(async () => {
      root.render(<LibraryFederationPanel projectId="project-1" />)
    })
    await flushPromises()
    await flushPromises()

    expect(container.textContent).toContain('Team AKS')
    expect(container.textContent).toContain('credentials configured')
    expect(container.textContent).not.toContain('secret-token')

    const probeButton = findButton('Probe Remote')
    expect(probeButton).toBeDefined()
    await act(async () => {
      probeButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()
    expect(fetchMock.mock.calls.some(([input]) => String(input).includes('/api/v1/library/remotes/remote-1/probe'))).toBe(true)

    const saveButton = findButton('Save Remote')
    expect(saveButton).toBeDefined()
    await act(async () => {
      saveButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()
    const updateCall = fetchMock.mock.calls.find(([input, init]) => String(input).includes('/api/v1/library/remotes/remote-1') && init?.method === 'PUT')
    expect(updateCall).toBeDefined()
    expect(String(updateCall?.[1]?.body || '')).not.toContain('secret-token')

    const deleteButton = findButton('Delete')
    expect(deleteButton).toBeDefined()
    await act(async () => {
      deleteButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()
    expect(fetchMock.mock.calls.some(([input, init]) => String(input).includes('/api/v1/library/remotes/remote-1') && init?.method === 'DELETE')).toBe(true)
  })
})

function remotePayload() {
  return {
    id: 'remote-1',
    name: 'Team AKS',
    base_url: 'https://apexload.example.test',
    default_project_id: 'project-1',
    auth_method: 'service_account',
    credential_ref: 'keyring:library-remote:remote-1',
    credential_configured: true,
    status: 'healthy',
  }
}

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
