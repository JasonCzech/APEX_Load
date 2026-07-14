// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi, type MockInstance } from 'vitest'

import GitHubSyncModal from './GitHubSyncModal'
import { GitHubConfigResponse, StoredScript } from '../types'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const config: GitHubConfigResponse = {
  enabled: true,
  oauth_configured: true,
  encryption_available: true,
  api_base_url: 'https://ghe.example.com/api/v3',
  web_base_url: 'https://ghe.example.com',
  scopes: ['repo'],
  default_repo: 'org/scripts',
  default_branch: 'main',
  path_template: 'apexload/scripts/{project}/{ref_key}.json',
  tls_insecure_skip_verify: false,
}

const script: StoredScript = {
  id: 'script-1',
  ref_key: 'S-1',
  name: 'Order Flow',
  protocol: 'http',
  version: '1.0.0',
  project_id: 'team-a',
  created_at: new Date().toISOString(),
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

describe('GitHubSyncModal (push)', () => {
  let container: HTMLDivElement
  let root: Root
  let fetchMock: MockInstance<(input: RequestInfo | URL, init?: RequestInit) => Promise<Response>>
  let pushBody: unknown = null

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    pushBody = null
    fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
      const url = String(input)
      const method = init?.method || 'GET'
      if (url.includes('/integrations/github/repos')) {
        return jsonResponse({ repos: [{ name: 'scripts', full_name: 'org/scripts', owner: 'org', private: true, default_branch: 'main', html_url: '' }] })
      }
      if (url.includes('/integrations/github/branches')) {
        return jsonResponse({ branches: [{ name: 'main', commit_sha: 'abc' }, { name: 'dev', commit_sha: 'def' }] })
      }
      if (url.includes('/github/status')) {
        return jsonResponse({ linked: false, status: 'not_linked' })
      }
      if (url.includes('/github/push') && method === 'POST') {
        pushBody = init?.body ? JSON.parse(String(init.body)) : null
        return jsonResponse({ pushed: true, strategy: 'commit', owner: 'org', repo: 'scripts', branch: 'main', path: 'p', commit_sha: 'c1', file_sha: 'f1', html_url: '' })
      }
      return jsonResponse({})
    })
  })

  afterEach(async () => {
    await act(async () => { root.unmount() })
    container.remove()
    fetchMock.mockRestore()
  })

  it('lists repos/branches and pushes a direct commit', async () => {
    const onPushed = vi.fn()
    const onClose = vi.fn()
    await act(async () => {
      root.render(<GitHubSyncModal mode="push" config={config} script={script} projectId="team-a" onClose={onClose} onPushed={onPushed} />)
    })
    await flushPromises()
    await flushPromises()
    await flushPromises()
    await act(async () => {
      await new Promise(resolve => window.setTimeout(resolve, 0))
    })

    expect(container.textContent).toContain('Push "Order Flow" to GitHub')
    const dialog = container.querySelector<HTMLElement>('[role="dialog"]')
    const repositorySelect = container.querySelector<HTMLSelectElement>('select')
    expect(dialog?.getAttribute('aria-modal')).toBe('true')
    expect(document.getElementById(dialog?.getAttribute('aria-labelledby') || '')?.textContent).toContain('Push "Order Flow" to GitHub')
    expect(document.activeElement).toBe(repositorySelect)
    const commit = findButton('Commit')
    expect(commit).toBeTruthy()

    await act(async () => { commit?.click() })
    await flushPromises()

    expect(onPushed).toHaveBeenCalled()
    expect(onClose).toHaveBeenCalled()
    expect(pushBody).toMatchObject({ owner: 'org', repo: 'scripts', branch: 'main', strategy: 'commit' })
  })

  it('closes with Escape through the shared dialog behavior', async () => {
    const onClose = vi.fn()
    await act(async () => {
      root.render(<GitHubSyncModal mode="push" config={config} script={script} projectId="team-a" onClose={onClose} />)
    })
    await flushPromises()
    await act(async () => {
      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    })
    expect(onClose).toHaveBeenCalledOnce()
  })

  it('announces repository-loading failures', async () => {
    fetchMock.mockRejectedValue(new Error('network unavailable'))
    await act(async () => {
      root.render(<GitHubSyncModal mode="push" config={config} script={script} projectId="team-a" onClose={() => {}} />)
    })
    await flushPromises()
    expect(container.querySelector('[role="alert"]')?.textContent).toBe('network unavailable')
  })
})
