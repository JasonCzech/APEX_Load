// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { ScriptDefinition } from '../types/scripts'
import ScriptSecretBindingsDialog from './ScriptSecretBindingsDialog'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const apiMock = vi.hoisted(() => ({
  getScriptSecretStatus: vi.fn(),
  getProjectSecretStatuses: vi.fn(),
  listProjectSecrets: vi.fn(),
  getProjectSecretUsage: vi.fn(),
  createProjectSecrets: vi.fn(),
  rebindScriptSecretReference: vi.fn(),
}))

vi.mock('../hooks/useApi', () => ({ useApi: () => apiMock }))

const script: ScriptDefinition = {
  id: 'script-1',
  project_id: 'project-1',
  updated_at: '2026-08-19T12:00:00Z',
  name: 'Checkout Flow',
  version: '1',
  protocol: 'http',
  config: { base_url: 'https://example.test' },
  actions: [{ name: 'Login', type: 'request', target: '/login', headers: { Authorization: 'Bearer {{secret.LOGIN_TOKEN}}' } }],
}

function setInput(input: HTMLInputElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')?.set
  setter?.call(input, value)
  input.dispatchEvent(new Event('input', { bubbles: true }))
}

async function flush() {
  await act(async () => { await Promise.resolve() })
}

describe('ScriptSecretBindingsDialog', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    vi.resetAllMocks()
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    apiMock.getScriptSecretStatus.mockResolvedValue({
      script_id: 'script-1', project_id: 'project-1', required: ['LOGIN_TOKEN'], configured: [], missing: ['LOGIN_TOKEN'],
      secrets: [{ name: 'LOGIN_TOKEN', configured: false }],
      bindings: [{ name: 'LOGIN_TOKEN', configured: false, occurrence_count: 1, locations: ['/actions/0/headers/Authorization'] }],
      capabilities: { provider: 'database', writable: true, create_supported: true, replace_supported: true },
    })
    apiMock.getProjectSecretStatuses.mockImplementation(async (_projectId: string, names: string[]) => ({
      project_id: 'project-1',
      capabilities: { provider: 'database', writable: true, create_supported: true, replace_supported: true },
      secrets: names.map(name => ({ name, configured: false })),
    }))
    apiMock.listProjectSecrets.mockResolvedValue({
      project_id: 'project-1',
      capabilities: { provider: 'database', writable: true, create_supported: true, replace_supported: true },
      secrets: [{ name: 'SHARED_LOGIN', provider: 'database', writable: true }],
      count: 1,
    })
    apiMock.getProjectSecretUsage.mockImplementation(async (_projectId: string, name: string) => ({
      project_id: 'project-1', name, usage_revision: `revision-${name}`, consumer_count: name === 'LOGIN_TOKEN' ? 1 : 0,
      reference_count: name === 'LOGIN_TOKEN' ? 1 : 0,
      consumers: name === 'LOGIN_TOKEN' ? [{ script_id: 'script-1', script_name: 'Checkout Flow', reference_count: 1 }] : [],
      limit: 50, offset: 0, has_more: false,
    }))
    apiMock.createProjectSecrets.mockResolvedValue({ project_id: 'project-1', secrets: [], count: 1 })
    apiMock.rebindScriptSecretReference.mockImplementation(async (_script: ScriptDefinition, oldName: string, newName: string) => ({
      script: { ...script, actions: [{ ...script.actions[0], headers: { Authorization: `Bearer {{secret.${newName}}}` } }] },
      old_name: oldName,
      new_name: newName,
      changed_locations: ['/actions/0/headers/Authorization'],
      changed_count: 1,
    }))
  })

  afterEach(async () => {
    await act(async () => root.unmount())
    container.remove()
    document.body.querySelectorAll('.script-secret-bindings-overlay').forEach(element => element.remove())
  })

  it('creates a missing value for the exact read-only script reference', async () => {
    const onConfigured = vi.fn()
    await act(async () => {
      root.render(<ScriptSecretBindingsDialog scriptId="script-1" script={script} projectId="project-1" scriptName="Checkout Flow" onClose={vi.fn()} onConfigured={onConfigured} onRebound={vi.fn()} />)
    })
    await flush()

    const nameInput = document.body.querySelector<HTMLInputElement>('input[type="text"]')
    expect(nameInput?.value).toBe('LOGIN_TOKEN')
    expect(nameInput?.readOnly).toBe(true)
    const valueInput = document.body.querySelector<HTMLInputElement>('input[type="password"]')!
    act(() => setInput(valueInput, 'one-time-value'))
    const configure = Array.from(document.body.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Configure exact reference')
    await act(async () => configure?.click())

    expect(apiMock.createProjectSecrets).toHaveBeenCalledWith('project-1', [{
      name: 'LOGIN_TOKEN',
      value: 'one-time-value',
      expected_usage: { usage_revision: 'revision-LOGIN_TOKEN', exclusive_script_id: 'script-1' },
    }])
    expect(onConfigured).toHaveBeenCalled()
    expect(document.body.textContent).not.toContain('one-time-value')
  })

  it('shows friendly importer names while retaining exact shared binding identities', async () => {
    const passwordName = 'PASSWORD_13860464B9306046D342E1F5'
    const usernameName = 'USERNAME_0B2497BE76736358F5C883E7'
    const importedScript: ScriptDefinition = {
      ...script,
      actions: [{
        name: 'Login',
        type: 'request',
        target: '/login',
        headers: {
          'X-Username': `{{secret.${usernameName}}}`,
          'X-Password': `{{secret.${passwordName}}}`,
        },
      }],
    }
    apiMock.getProjectSecretUsage.mockImplementation(async (_projectId: string, name: string) => ({
      project_id: 'project-1', name, usage_revision: `revision-${name}`, consumer_count: 40, reference_count: 40,
      consumers: [{ script_id: 'script-1', script_name: 'AuthenticateMobilitySubscriber_312', reference_count: 1 }],
      limit: 50, offset: 0, has_more: false,
    }))

    await act(async () => {
      root.render(<ScriptSecretBindingsDialog scriptId="script-1" script={importedScript} projectId="project-1" scriptName="AuthenticateMobilitySubscriber_312" onClose={vi.fn()} onRebound={vi.fn()} />)
    })
    await flush()

    const rows = Array.from(document.body.querySelectorAll<HTMLElement>('.script-secret-binding-row'))
    expect(rows.map(row => row.querySelector('.script-secret-binding-summary strong')?.textContent)).toEqual(['PASSWORD', 'USERNAME'])
    expect(rows.map(row => row.querySelector<HTMLInputElement>('input[type="text"]')?.value)).toEqual(['PASSWORD', 'USERNAME'])
    expect(rows.map(row => row.querySelector('.secret-binding-review-reference code')?.textContent)).toEqual([
      '{{secret.PASSWORD}}',
      '{{secret.USERNAME}}',
    ])
    expect(document.body.textContent).not.toContain(passwordName)
    expect(document.body.textContent).not.toContain(usernameName)

    const passwordRow = rows[0]
    const acknowledgement = passwordRow.querySelector<HTMLLabelElement>('.script-secret-impact-acknowledgement')!
    const checkbox = acknowledgement.querySelector<HTMLInputElement>('input[type="checkbox"]')!
    expect(acknowledgement.firstElementChild).toBe(checkbox)
    expect(checkbox.nextElementSibling?.textContent).toBe('Use this value for all 40 consuming scripts')

    act(() => {
      setInput(passwordRow.querySelector<HTMLInputElement>('input[type="password"]')!, 'shared-value')
      checkbox.click()
    })
    const configure = Array.from(passwordRow.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Configure shared value')
    await act(async () => configure?.click())

    expect(apiMock.createProjectSecrets).toHaveBeenCalledWith('project-1', [{
      name: passwordName,
      value: 'shared-value',
      expected_usage: { usage_revision: `revision-${passwordName}` },
    }])
  })

  it('rebinds every exact occurrence to an explicitly selected existing project secret', async () => {
    const onRebound = vi.fn()
    const onClose = vi.fn()
    await act(async () => {
      root.render(<ScriptSecretBindingsDialog scriptId="script-1" script={script} projectId="project-1" scriptName="Checkout Flow" onClose={onClose} onRebound={onRebound} />)
    })
    await flush()

    const select = document.body.querySelector<HTMLSelectElement>('select')!
    act(() => {
      const setter = Object.getOwnPropertyDescriptor(window.HTMLSelectElement.prototype, 'value')?.set
      setter?.call(select, 'SHARED_LOGIN')
      select.dispatchEvent(new Event('change', { bubbles: true }))
    })
    const rebind = Array.from(document.body.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Rebind and share')
    await act(async () => rebind?.click())

    expect(apiMock.rebindScriptSecretReference).toHaveBeenCalledWith(script, 'LOGIN_TOKEN', 'SHARED_LOGIN')
    expect(onRebound).toHaveBeenCalledWith(expect.objectContaining({ actions: expect.any(Array) }), ['/actions/0/headers/Authorization'])
    expect(onClose).toHaveBeenCalled()
  })

  it('retains and reports an unused dedicated value when the later script save fails', async () => {
    apiMock.getProjectSecretUsage.mockImplementation(async (_projectId: string, name: string) => ({
      project_id: 'project-1', name, usage_revision: `revision-${name}`,
      consumer_count: name === 'LOGIN_TOKEN' ? 2 : 0,
      reference_count: name === 'LOGIN_TOKEN' ? 2 : 0,
      consumers: name === 'LOGIN_TOKEN'
        ? [{ script_id: 'script-1', script_name: 'Checkout Flow', reference_count: 1 }, { script_id: 'script-2', script_name: 'Checkout Clone', reference_count: 1 }]
        : [],
      limit: 50, offset: 0, has_more: false,
    }))
    const onRebound = vi.fn().mockRejectedValue(new Error('save failed'))
    await act(async () => {
      root.render(<ScriptSecretBindingsDialog scriptId="script-1" script={script} projectId="project-1" scriptName="Checkout Flow" onClose={vi.fn()} onRebound={onRebound} />)
    })
    await flush()
    const valueInput = document.body.querySelector<HTMLInputElement>('input[type="password"]')!
    act(() => setInput(valueInput, 'dedicated-value'))
    const dedicated = Array.from(document.body.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Create dedicated value')
    await act(async () => dedicated?.click())

    expect(apiMock.createProjectSecrets).toHaveBeenCalledWith('project-1', [{
      name: 'LOGIN_TOKEN_CHECKOUT_FLOW',
      value: 'dedicated-value',
      expected_usage: { usage_revision: 'revision-LOGIN_TOKEN_CHECKOUT_FLOW' },
    }])
    expect(onRebound).toHaveBeenCalledWith(
      expect.objectContaining({ actions: expect.any(Array) }),
      ['/actions/0/headers/Authorization'],
      { createdSecretName: 'LOGIN_TOKEN_CHECKOUT_FLOW' },
    )
    expect(document.body.textContent).toContain('remains as an unused vault entry')
    expect(document.body.textContent).not.toContain('dedicated-value')
  })

  it('bounds dedicated names derived from unusually long script names', async () => {
    apiMock.getProjectSecretUsage.mockImplementation(async (_projectId: string, name: string) => ({
      project_id: 'project-1', name, usage_revision: `revision-${name}`,
      consumer_count: name === 'LOGIN_TOKEN' ? 2 : 0,
      reference_count: name === 'LOGIN_TOKEN' ? 2 : 0,
      consumers: name === 'LOGIN_TOKEN'
        ? [{ script_id: 'script-1', script_name: 'Long name', reference_count: 1 }, { script_id: 'script-2', script_name: 'Clone', reference_count: 1 }]
        : [],
      limit: 50, offset: 0, has_more: false,
    }))
    await act(async () => {
      root.render(<ScriptSecretBindingsDialog scriptId="script-1" script={script} projectId="project-1" scriptName={'X'.repeat(400)} onClose={vi.fn()} onRebound={vi.fn()} />)
    })
    await flush()
    const valueInput = document.body.querySelector<HTMLInputElement>('input[type="password"]')!
    act(() => setInput(valueInput, 'bounded-value'))
    const dedicated = Array.from(document.body.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Create dedicated value')
    await act(async () => dedicated?.click())

    const calls = apiMock.createProjectSecrets.mock.calls
    const createdName = calls[calls.length - 1]?.[1]?.[0]?.name as string
    expect(createdName.length).toBeLessThanOrEqual(249)
    expect(createdName).toMatch(/^L_X+$/)
  })
})
