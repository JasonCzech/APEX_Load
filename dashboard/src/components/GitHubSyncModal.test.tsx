// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi, type MockInstance } from 'vitest'

import GitHubSyncModal from './GitHubSyncModal'
import { GitHubConfigResponse, StoredScript } from '../types'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const secretApiMock = vi.hoisted(() => ({
  getProjectSecretStatuses: vi.fn(),
  createProjectSecrets: vi.fn(),
  migrateImportSecrets: vi.fn(),
  discardImportSecretSession: vi.fn(),
  applyImportParameterization: vi.fn(),
}))

vi.mock('../hooks/useApi', () => ({
  useApi: () => secretApiMock,
}))

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
  let bindingApplyResponse: Promise<Response> | null
  let importParameterization: Record<string, unknown> | undefined

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
    bindingApplyResponse = null
    importParameterization = undefined
    secretApiMock.getProjectSecretStatuses.mockResolvedValue({
      project_id: 'team-a',
      capabilities: {
        provider: 'database',
        writable: true,
        create_supported: true,
        replace_supported: true,
        source_secret_migration_policy_enabled: false,
      },
      secrets: [],
    })
    secretApiMock.createProjectSecrets.mockResolvedValue({ project_id: 'team-a', secrets: [], count: 0 })
    secretApiMock.migrateImportSecrets.mockResolvedValue({ project_id: 'team-a', secrets: [], count: 0 })
    secretApiMock.applyImportParameterization.mockResolvedValue({ script: importedDraft, diffs: [], applied_count: 0 })
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
          parameterization: importParameterization,
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
        if (bindingApplyResponse) return bindingApplyResponse
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

  it('applies prefilled credential references from the primary save action', async () => {
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

    expect(container.textContent).toContain('Credentials (1)')
    expect(container.textContent).toContain('configure them later in Project Secrets')
    const bindingName = container.querySelector<HTMLInputElement>('.secret-binding-review input')
    expect(bindingName?.value).toBe('LOGIN_TOKEN')

    const save = findButton('Save with 1 secret to configure')
    expect(save?.disabled).toBe(false)
    await act(async () => { save?.click() })
    await flushPromises()
    await flushPromises()

    expect(applyBody).toEqual({
      script: importedDraft,
      bindings: [{ requirement_id: 'binding-1', secret_name: 'LOGIN_TOKEN' }],
    })
    expect(createBody).toEqual({ script: resolvedDraft, project_id: 'team-a' })
    expect(JSON.stringify(createBody)).not.toContain('[REDACTED]')
    expect(JSON.stringify(createBody)).toContain('{{secret.LOGIN_TOKEN}}')
    expect(onImported).toHaveBeenCalledWith(expect.objectContaining({ id: 'script-imported' }))
    expect(onClose).toHaveBeenCalledOnce()
  })

  it('ignores an import response after the selected branch changes', async () => {
    let resolveImport: ((response: Response) => void) | undefined
    const importResponse = new Promise<Response>(resolve => { resolveImport = resolve })
    await act(async () => {
      root.render(<GitHubSyncModal mode="import" config={config} projectId="team-a" onClose={() => {}} />)
    })
    await flushPromises()
    await flushPromises()
    await act(async () => { await new Promise(resolve => window.setTimeout(resolve, 0)) })
    await act(async () => container.querySelector<HTMLButtonElement>('[aria-label="Browse scripts/redacted.json"]')?.click())

    fetchMock.mockImplementationOnce(() => importResponse)
    await act(async () => {
      findButton('Import & Save')?.click()
      await Promise.resolve()
    })
    const branchSelect = Array.from(container.querySelectorAll<HTMLSelectElement>('select'))[1]
    await act(async () => {
      if (branchSelect) {
        const setter = Object.getOwnPropertyDescriptor(window.HTMLSelectElement.prototype, 'value')?.set
        setter?.call(branchSelect, 'dev')
        branchSelect.dispatchEvent(new Event('change', { bubbles: true }))
      }
    })
    await act(async () => {
      resolveImport?.(new Response(JSON.stringify({
        script: importedDraft,
        secret_binding_requirements: [{ id: 'late', path: '/actions/0', suggested_name: 'LATE_TOKEN' }],
      }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
      await Promise.resolve()
    })

    expect(container.textContent).not.toContain('Credentials (1)')
    expect(container.textContent).not.toContain('LATE_TOKEN')
    expect(createBody).toBeNull()
  })

  it('does not create a script when the modal is cancelled during import', async () => {
    let resolveImport: ((response: Response) => void) | undefined
    const importResponse = new Promise<Response>(resolve => { resolveImport = resolve })
    const onImported = vi.fn()
    const onClose = vi.fn()
    await act(async () => {
      root.render(<GitHubSyncModal mode="import" config={config} projectId="team-a" onClose={onClose} onImported={onImported} />)
    })
    await flushPromises()
    await flushPromises()
    await act(async () => { await new Promise(resolve => window.setTimeout(resolve, 0)) })
    await act(async () => container.querySelector<HTMLButtonElement>('[aria-label="Browse scripts/redacted.json"]')?.click())

    fetchMock.mockImplementationOnce(() => importResponse)
    await act(async () => {
      findButton('Import & Save')?.click()
      await Promise.resolve()
    })
    await act(async () => {
      findButton('Cancel')?.click()
    })
    expect(onClose).toHaveBeenCalledOnce()

    await act(async () => {
      resolveImport?.(new Response(JSON.stringify({
        script: resolvedDraft,
        secret_binding_requirements: [],
      }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
      await importResponse
      await Promise.resolve()
    })

    expect(createBody).toBeNull()
    expect(onImported).not.toHaveBeenCalled()
  })

  it('locks parameterization while credential bindings are being prepared for save', async () => {
    const onImported = vi.fn()
    const onClose = vi.fn()
    const parameterizedDraft = {
      ...importedDraft,
      actions: [{ ...importedDraft.actions[0], target: '{{env.LOGIN_URL}}' }],
    }
    importParameterization = {
      suggestions: [{
        id: 'param-login-url',
        kind: 'env',
        var_name: 'LOGIN_URL',
        replacement: '{{env.LOGIN_URL}}',
        confidence: 0.95,
        rule_id: 'url.constant',
        rationale: 'Use the project login URL.',
        auto_apply: true,
        high_risk: false,
      }],
      summary: { total: 1, by_kind: { env: 1 }, auto_apply_count: 1, review_count: 0, high_risk_count: 0 },
    }
    secretApiMock.applyImportParameterization.mockResolvedValue({
      script: parameterizedDraft,
      diffs: [{ kind: 'value_replaced', path: '/actions/0/target', suggestion_id: 'param-login-url' }],
      applied_count: 1,
    })
    let resolveBindingApply!: (response: Response) => void
    bindingApplyResponse = new Promise<Response>(resolve => {
      resolveBindingApply = resolve
    })

    await act(async () => {
      root.render(<GitHubSyncModal mode="import" config={config} projectId="team-a" onClose={onClose} onImported={onImported} />)
    })
    await flushPromises()
    await flushPromises()
    await act(async () => {
      await new Promise(resolve => window.setTimeout(resolve, 0))
    })
    const file = container.querySelector<HTMLButtonElement>('[aria-label="Browse scripts/redacted.json"]')
    await act(async () => { file?.click() })
    await act(async () => { findButton('Import & Save')?.click() })
    await flushPromises()
    await flushPromises()

    await act(async () => { findButton('Save with 1 secret to configure')?.click() })
    await flushPromises()
    await flushPromises()
    expect(applyBody).not.toBeNull()

    const parameterizationApply = findButton('Apply Selected (1)')
    expect(parameterizationApply?.disabled).toBe(true)
    await act(async () => { parameterizationApply?.click() })
    await flushPromises()
    expect(secretApiMock.applyImportParameterization).not.toHaveBeenCalled()

    await act(async () => {
      resolveBindingApply(new Response(JSON.stringify({
        script: resolvedDraft,
        secret_binding_requirements: [],
      }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
      await bindingApplyResponse
    })
    await flushPromises()
    await flushPromises()

    expect(createBody).toEqual({ script: resolvedDraft, project_id: 'team-a' })
    expect(onImported).toHaveBeenCalledWith(expect.objectContaining({ id: 'script-imported' }))
    expect(onClose).toHaveBeenCalledOnce()
  })
})
