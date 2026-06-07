// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi, type MockInstance } from 'vitest'

import GitHubIntegrationPanel from './GitHubIntegrationPanel'
import { DashboardGitHubSettings } from '../types'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

function settings(overrides: Partial<DashboardGitHubSettings> = {}): DashboardGitHubSettings {
  return {
    enabled: true,
    client_id: 'client-1',
    client_secret_env_var: 'APEXLOAD_GITHUB_CLIENT_SECRET',
    api_base_url: 'https://ghe.example.com/api/v3',
    web_base_url: 'https://ghe.example.com',
    ca_cert_file: '',
    tls_insecure_skip_verify: false,
    scopes: ['repo'],
    default_repo: 'org/scripts',
    default_branch: 'main',
    path_template: 'apexload/scripts/{project}/{ref_key}.json',
    ...overrides,
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

describe('GitHubIntegrationPanel', () => {
  let container: HTMLDivElement
  let root: Root
  let fetchMock: MockInstance<(input: RequestInfo | URL, init?: RequestInit) => Promise<Response>>

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
      const url = String(input)
      if (url.includes('/api/v1/integrations/github/connection')) {
        return jsonResponse({ connected: true, login: 'octocat', scopes: 'repo' })
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

  it('shows the connected GitHub identity and renders the admin form', async () => {
    await act(async () => {
      root.render(<GitHubIntegrationPanel value={settings()} onChange={() => {}} />)
    })
    await flushPromises()
    await flushPromises()

    expect(container.textContent).toContain('Connected as octocat')
    expect(findButton('Disconnect')).toBeTruthy()
    const apiBase = container.querySelector('input[value="https://ghe.example.com/api/v3"]')
    expect(apiBase).toBeTruthy()
  })

  it('emits config changes via onChange when toggling enabled', async () => {
    const onChange = vi.fn()
    await act(async () => {
      root.render(<GitHubIntegrationPanel value={settings({ enabled: false })} onChange={onChange} />)
    })
    await flushPromises()

    const checkbox = container.querySelector('input[type="checkbox"]') as HTMLInputElement
    await act(async () => {
      checkbox.click()
    })
    expect(onChange).toHaveBeenCalled()
    const next = onChange.mock.calls[0][0] as DashboardGitHubSettings
    expect(next.enabled).toBe(true)
  })

  it('disables Connect when the integration is not enabled', async () => {
    fetchMock.mockImplementation(async () => jsonResponse({ connected: false }))
    await act(async () => {
      root.render(<GitHubIntegrationPanel value={settings({ enabled: false })} onChange={() => {}} />)
    })
    await flushPromises()
    await flushPromises()

    const connect = findButton('Connect GitHub')
    expect(connect).toBeTruthy()
    expect(connect?.disabled).toBe(true)
  })
})
