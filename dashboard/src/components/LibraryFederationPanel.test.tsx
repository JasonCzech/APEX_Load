// @vitest-environment jsdom

import { act, useState } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi, type MockInstance } from 'vitest'

import { NavigationGuardProvider } from '../hooks/NavigationGuardRoot'
import { useNavigationGuardRunner } from '../hooks/NavigationGuardProvider'
import type { LibraryRemote } from '../types/library'
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
      root.render(<LibraryFederationPanel projectId="project-1" applicationId="app-local" applicationName="Storefront" releaseId="release-local" releaseName="Sprint 24" />)
    })
    await flushPromises()
    await flushPromises()

    expect(container.textContent).toContain('Team AKS')
    expect(container.textContent).toContain('credentials configured')
    expect(container.textContent).not.toContain('secret-token')
    expect(findFieldInput('Local Application (Storefront)')?.value).toBe('app-local')
    expect(findFieldInput('Remote Application')?.value).toBe('app-remote')
    expect(findFieldInput('Local Release (Sprint 24)')?.value).toBe('release-local')
    expect(findFieldInput('Remote Release')?.value).toBe('release-remote')
    const credentialRef = Array.from(container.querySelectorAll('input')).find(input => input.placeholder.includes('APEXLOAD_LIBRARY_REMOTE_TOKEN'))
    expect(credentialRef?.placeholder).toBe('env:APEXLOAD_LIBRARY_REMOTE_TOKEN')

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
    const updateBody = JSON.parse(String(updateCall?.[1]?.body || '{}')) as { project_mappings?: Record<string, string>; application_mappings?: Record<string, string>; release_mappings?: Record<string, string> }
    expect(updateBody.project_mappings).toEqual({ 'project-1': 'project-1' })
    expect(updateBody.application_mappings).toEqual({ 'app-local': 'app-remote' })
    expect(updateBody.release_mappings).toEqual({ 'release-local': 'release-remote' })

    const deleteButton = findButton('Delete')
    expect(deleteButton).toBeDefined()
    await act(async () => {
      deleteButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()
    expect(fetchMock.mock.calls.some(([input, init]) => String(input).includes('/api/v1/library/remotes/remote-1') && init?.method === 'DELETE')).toBe(true)
  })

  it('uses the shared settings select style for the auth method dropdown', async () => {
    await act(async () => {
      root.render(<LibraryFederationPanel projectId="project-1" applicationId="app-local" applicationName="Storefront" releaseId="release-local" releaseName="Sprint 24" />)
    })
    await flushPromises()
    await flushPromises()

    const authMethodSelect = findFieldSelect('Auth Method')
    expect(authMethodSelect).toBeTruthy()
    expect(authMethodSelect?.classList.contains('settings-select')).toBe(true)
    expect(authMethodSelect?.value).toBe('service_account')

    // @ts-expect-error Vitest runs in Node, but the dashboard app tsconfig intentionally omits Node types.
    const { readFileSync } = await import('node:fs')
    const cwd = (globalThis as typeof globalThis & { process: { cwd: () => string } }).process.cwd()
    const stylesheet = readFileSync(`${cwd}/src/components/Pages.css`, 'utf8') as string
    const selectRule = stylesheet.match(/\.settings-select \{[\s\S]*?\n\}/)?.[0] ?? ''

    expect(selectRule).toContain('border: 1px solid var(--border);')
    expect(selectRule).toContain('background: var(--bg-secondary);')
    expect(selectRule).toContain('color: var(--text-primary);')
  })

  it('keeps a committed create selected when the reconciliation refresh fails', async () => {
    await act(async () => {
      root.render(<LibraryFederationPanel projectId="project-1" applicationId="app-local" applicationName="Storefront" releaseId="release-local" releaseName="Sprint 24" />)
    })
    await flushPromises()
    await flushPromises()

    await act(async () => {
      findButton('New Remote')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await changeInput(findFieldInput('Remote Name'), 'Disaster Recovery')
    await changeInput(findFieldInput('Base URL'), 'https://dr.example.test')
    await changeInput(findFieldInput('Remote Project'), 'project-remote')

    let createCalls = 0
    let updateCalls = 0
    fetchMock.mockImplementation(async (input, init) => {
      const url = String(input)
      const method = init?.method || 'GET'
      if (url.endsWith('/api/v1/library/remotes') && method === 'POST') {
        createCalls += 1
        return jsonResponse({
          ...remotePayload(),
          id: 'remote-new',
          name: 'Disaster Recovery',
          base_url: 'https://dr.example.test',
          default_project_id: 'project-remote',
          project_mappings: { 'project-1': 'project-remote' },
        })
      }
      if (url.includes('/api/v1/library/remotes/remote-new') && method === 'PUT') {
        updateCalls += 1
        return jsonResponse({
          ...remotePayload(),
          id: 'remote-new',
          name: 'Disaster Recovery',
          base_url: 'https://dr.example.test',
          default_project_id: 'project-remote',
          project_mappings: { 'project-1': 'project-remote' },
        })
      }
      if (url.endsWith('/api/v1/library/remotes') && method === 'GET') {
        throw new Error('refresh unavailable')
      }
      return jsonResponse({})
    })

    await act(async () => {
      findButton('Add Remote')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(createCalls).toBe(1)
    expect(container.textContent).toContain('Added Remote Workspace "Disaster Recovery".')
    expect(container.textContent).toContain('was saved, but the list could not be refreshed')
    expect(findButton('Save Remote')).toBeDefined()

    await act(async () => {
      findButton('Save Remote')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(createCalls).toBe(1)
    expect(updateCalls).toBe(1)
  })

  it('keeps the committed remote when a reconciliation refresh returns a stale version of the same row', async () => {
    await act(async () => {
      root.render(<LibraryFederationPanel projectId="project-1" applicationId="app-local" applicationName="Storefront" releaseId="release-local" releaseName="Sprint 24" />)
    })
    await flushPromises()
    await flushPromises()

    await changeInput(findFieldInput('Remote Application'), 'app-remote-next')
    await changeInput(findFieldInput('Remote Release'), 'release-remote-next')

    fetchMock.mockImplementation(async (input, init) => {
      const url = String(input)
      const method = init?.method || 'GET'
      if (url.includes('/api/v1/library/remotes/remote-1') && method === 'PUT') {
        return jsonResponse({
          ...remotePayload(),
          application_mappings: { 'app-local': 'app-remote-next' },
          release_mappings: { 'release-local': 'release-remote-next' },
        })
      }
      if (url.includes('/api/v1/library/remotes') && method === 'GET') {
        // Simulate a lagging replica returning the pre-mutation row.
        return jsonResponse({ remotes: [remotePayload()], count: 1 })
      }
      return jsonResponse({})
    })

    await act(async () => {
      findButton('Save Remote')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()
    await flushPromises()

    expect(findFieldInput('Remote Application')?.value).toBe('app-remote-next')
    expect(findFieldInput('Remote Release')?.value).toBe('release-remote-next')
  })

  it('guards mapping and credential drafts across remote, refresh, and context transitions', async () => {
    fetchMock.mockImplementation(async (input, init) => {
      const url = String(input)
      const method = init?.method || 'GET'
      if (url.includes('/api/v1/library/remotes') && method === 'GET') {
        return jsonResponse({
          remotes: [
            remotePayload(),
            remotePayload({
              id: 'remote-2',
              name: 'Team DR',
              base_url: 'https://dr.example.test',
              application_mappings: { 'app-local': 'app-dr' },
              release_mappings: { 'release-local': 'release-dr' },
            }),
          ],
          count: 2,
        })
      }
      return jsonResponse({})
    })

    await act(async () => {
      root.render(
        <NavigationGuardProvider>
          <GuardedFederationHarness />
        </NavigationGuardProvider>,
      )
    })
    await flushPromises()
    await flushPromises()

    await changeInput(findFieldInput('Remote Application'), 'unsaved-app')
    await act(async () => findRemoteButton('Team DR')?.click())

    expect(document.body.textContent).toContain('Remote workspace mapping and credential draft has unsaved changes.')
    expect(findFieldInput('Remote Application')?.value).toBe('unsaved-app')
    expect(findRemoteButton('Team AKS')?.classList.contains('active')).toBe(true)

    await act(async () => findButton('Keep editing')?.click())
    await act(async () => findButton('Refresh')?.click())
    expect(document.body.textContent).toContain('Remote workspace mapping and credential draft has unsaved changes.')
    expect(fetchMock.mock.calls.filter(([input, init]) => String(input).includes('/api/v1/library/remotes') && (init?.method || 'GET') === 'GET')).toHaveLength(1)

    await act(async () => findButton('Discard changes')?.click())
    await flushPromises()
    expect(findFieldInput('Remote Application')?.value).toBe('app-remote')

    await changeInput(findFieldInput('Credential Ref'), 'env:ROTATED_REMOTE_TOKEN')
    await act(async () => findButton('Switch Context')?.click())
    expect(document.body.textContent).toContain('Remote workspace mapping and credential draft has unsaved changes.')
    expect(findFieldInput('Credential Ref')?.value).toBe('env:ROTATED_REMOTE_TOKEN')

    await act(async () => findButton('Discard changes')?.click())
    await flushPromises()
    expect(findFieldInput('Local Application (Billing)')?.value).toBe('app-billing')
    expect(findFieldInput('Remote Application')?.value).toBe('')
  })

  it('ignores older overlapping remote-list responses', async () => {
    await act(async () => {
      root.render(<LibraryFederationPanel projectId="project-1" applicationId="app-local" releaseId="release-local" />)
    })
    await flushPromises()
    await flushPromises()

    const firstRefresh = deferred<Response>()
    const secondRefresh = deferred<Response>()
    let refreshCalls = 0
    fetchMock.mockImplementation((input, init) => {
      const url = String(input)
      const method = init?.method || 'GET'
      if (url.includes('/api/v1/library/remotes') && method === 'GET') {
        refreshCalls += 1
        return refreshCalls === 1 ? firstRefresh.promise : secondRefresh.promise
      }
      return jsonResponse({})
    })

    const refresh = findButton('Refresh')
    await act(async () => {
      refresh?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      refresh?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(refreshCalls).toBe(2)

    await act(async () => {
      secondRefresh.resolve(await jsonResponse({ remotes: [remotePayload({ name: 'Fresh Remote' })], count: 1 }))
    })
    await flushPromises()
    expect(findRemoteButton('Fresh Remote')).toBeDefined()

    await act(async () => {
      firstRefresh.resolve(await jsonResponse({ remotes: [remotePayload({ name: 'Stale Remote' })], count: 1 }))
    })
    await flushPromises()
    expect(findRemoteButton('Fresh Remote')).toBeDefined()
    expect(findRemoteButton('Stale Remote')).toBeUndefined()
  })

  it('keeps a deleted remote hidden while list replicas still return it', async () => {
    await act(async () => {
      root.render(<LibraryFederationPanel projectId="project-1" applicationId="app-local" releaseId="release-local" />)
    })
    await flushPromises()
    await flushPromises()

    fetchMock.mockImplementation(async (input, init) => {
      const url = String(input)
      const method = init?.method || 'GET'
      if (url.includes('/api/v1/library/remotes/remote-1') && method === 'DELETE') {
        return jsonResponse({ deleted: 'remote-1' })
      }
      if (url.includes('/api/v1/library/remotes') && method === 'GET') {
        return jsonResponse({ remotes: [remotePayload()], count: 1 })
      }
      return jsonResponse({})
    })

    await act(async () => findButton('Delete')?.click())
    await flushPromises()
    await flushPromises()

    expect(container.querySelectorAll('.settings-release-row')).toHaveLength(0)
    expect(findButton('Delete')).toBeUndefined()

    await act(async () => findButton('Refresh')?.click())
    await flushPromises()
    expect(container.querySelectorAll('.settings-release-row')).toHaveLength(0)
  })
})

function GuardedFederationHarness() {
  const [billingContext, setBillingContext] = useState(false)
  const { runGuarded } = useNavigationGuardRunner()
  return (
    <>
      <button type="button" onClick={() => runGuarded(() => setBillingContext(true))}>Switch Context</button>
      <LibraryFederationPanel
        projectId="project-1"
        applicationId={billingContext ? 'app-billing' : 'app-local'}
        applicationName={billingContext ? 'Billing' : 'Storefront'}
        releaseId={billingContext ? 'release-billing' : 'release-local'}
        releaseName={billingContext ? 'Sprint 25' : 'Sprint 24'}
      />
    </>
  )
}

function remotePayload(overrides: Partial<LibraryRemote> = {}): LibraryRemote {
  return {
    id: 'remote-1',
    name: 'Team AKS',
    base_url: 'https://apexload.example.test',
    default_project_id: 'project-1',
    project_mappings: { 'project-1': 'project-1' },
    application_mappings: { 'app-local': 'app-remote' },
    release_mappings: { 'release-local': 'release-remote' },
    auth_method: 'service_account',
    credential_ref: 'keyring:library-remote:remote-1',
    credential_configured: true,
    status: 'healthy',
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

function findRemoteButton(label: string) {
  return Array.from(document.querySelectorAll<HTMLButtonElement>('.settings-release-row'))
    .find(button => button.textContent?.includes(label))
}

function findFieldSelect(label: string) {
  return Array.from(document.querySelectorAll('label')).find(field => (
    field.querySelector('span')?.textContent?.trim() === label
  ))?.querySelector('select') as HTMLSelectElement | null
}

function findFieldInput(label: string) {
  return Array.from(document.querySelectorAll('label')).find(field => (
    field.querySelector('span')?.textContent?.trim() === label
  ))?.querySelector('input') as HTMLInputElement | null
}

async function changeInput(input: HTMLInputElement | null | undefined, value: string) {
  expect(input).toBeTruthy()
  await act(async () => {
    const valueSetter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set
    valueSetter?.call(input, value)
    input?.dispatchEvent(new Event('input', { bubbles: true }))
    input?.dispatchEvent(new Event('change', { bubbles: true }))
  })
}

async function flushPromises() {
  await act(async () => {
    await Promise.resolve()
  })
}

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>(next => {
    resolve = next
  })
  return { promise, resolve }
}
