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

describe('GitHubSyncModal (import)', () => {
  let container: HTMLDivElement
  let root: Root
  let fetchMock: MockInstance<(input: RequestInfo | URL, init?: RequestInit) => Promise<Response>>
  let applyBody: Record<string, unknown> | null
  let createBody: Record<string, unknown> | null

  const importedDraft = {
    name: 'Redacted Login',
    version: '1.0.0',
    protocol: 'http',
    project_id: 'team-a',
    config: {},
    actions: [{
      name: 'Login',
      type: 'http',
      method: 'POST',
      target: 'https://example.test/login',
      headers: { Authorization: 'Bearer [REDACTED]' },
    }],
  }
  const resolvedDraft = {
    ...importedDraft,
    actions: [{
      ...importedDraft.actions[0],
      headers: { Authorization: 'Bearer {{secret.LOGIN_TOKEN}}' },
    }],
  }

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    applyBody = null
    createBody = null
    fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
      const url = String(input)
      const method = init?.method || 'GET'
      if (url.includes('/integrations/github/repos')) {
        return jsonResponse({ repos: [{ name: 'scripts', full_name: 'org/scripts', owner: 'org', private: true, default_branch: 'main', html_url: '' }] })
      }
      if (url.includes('/integrations/github/branches')) {
        return jsonResponse({ branches: [{ name: 'main', commit_sha: 'abc' }] })
      }
      if (url.includes('/integrations/github/contents')) {
        return jsonResponse({
          type: 'dir',
          entries: [{ name: 'redacted.json', path: 'scripts/redacted.json', type: 'file', sha: 'file-1', size: 512 }],
        })
      }
      if (url.includes('/scripts/import/github') && method === 'POST') {
        return jsonResponse({
          script: importedDraft,
          secret_binding_requirements: [{
            id: 'binding-1',
            path: 'actions[0].headers.Authorization',
            label: 'Login · Authorization header',
            context: 'Bearer credential in the Login action',
            suggested_name: 'LOGIN_TOKEN',
            replacement: 'Bearer {{secret.LOGIN_TOKEN}}',
          }],
        })
      }
      if (url.includes('/scripts/secret-bindings/apply') && method === 'POST') {
        applyBody = init?.body ? JSON.parse(String(init.body)) : null
        return jsonResponse({ script: resolvedDraft, secret_binding_requirements: [] })
      }
      if (url.endsWith('/scripts') && method === 'POST') {
        createBody = init?.body ? JSON.parse(String(init.body)) : null
        return jsonResponse({
          id: 'script-imported',
          ref_key: 'S-2',
          name: resolvedDraft.name,
          protocol: resolvedDraft.protocol,
          version: resolvedDraft.version,
          project_id: 'team-a',
          created_at: new Date().toISOString(),
        })
      }
      return jsonResponse({})
    })
  })

  afterEach(async () => {
    await act(async () => { root.unmount() })
    container.remove()
    fetchMock.mockRestore()
  })

  it('requires redacted credentials to be bound before saving the imported script', async () => {
    const onImported = vi.fn()
    const onClose = vi.fn()
    await act(async () => {
      root.render(<GitHubSyncModal mode="import" config={config} projectId="team-a" onClose={onClose} onImported={onImported} />)
    })
    await flushPromises()
    await flushPromises()
    await act(async () => {
      await new Promise(resolve => window.setTimeout(resolve, 0))
    })

    const file = container.querySelector<HTMLButtonElement>('[aria-label="Browse scripts/redacted.json"]')
    expect(file).toBeTruthy()
    await act(async () => { file?.click() })

    const importButton = findButton('Import & Save')
    expect(importButton?.disabled).toBe(false)
    await act(async () => { importButton?.click() })
    await flushPromises()
    await flushPromises()

    expect(container.textContent).toContain('Resolve Redacted Credentials (1)')
    expect(container.textContent).toContain('never a credential value')
    const bindingName = container.querySelector<HTMLInputElement>('.secret-binding-review input')
    expect(bindingName?.value).toBe('LOGIN_TOKEN')

    const saveBeforeBinding = findButton('Save Script')
    expect(saveBeforeBinding?.disabled).toBe(true)
    await act(async () => { saveBeforeBinding?.click() })
    expect(createBody).toBeNull()

    const apply = findButton('Apply Bindings (1)')
    expect(apply?.disabled).toBe(false)
    await act(async () => { apply?.click() })
    await flushPromises()
    await flushPromises()

    expect(applyBody).toEqual({
      script: importedDraft,
      bindings: [{ requirement_id: 'binding-1', secret_name: 'LOGIN_TOKEN' }],
    })
    expect(container.textContent).toContain('All redacted values now use secret references.')

    const save = findButton('Save Script')
    expect(save?.disabled).toBe(false)
    await act(async () => { save?.click() })
    await flushPromises()
    await flushPromises()

    expect(createBody).toEqual({ script: resolvedDraft, project_id: 'team-a' })
    expect(JSON.stringify(createBody)).not.toContain('[REDACTED]')
    expect(JSON.stringify(createBody)).toContain('{{secret.LOGIN_TOKEN}}')
    expect(onImported).toHaveBeenCalledWith(expect.objectContaining({ id: 'script-imported' }))
    expect(onClose).toHaveBeenCalledOnce()
  })
})
