import { authoringFixture, savedAuthoringFixture } from '../test/scriptAuthoringFixtures'
import StoredScriptSecretBindingsDialog from './StoredScriptSecretBindingsDialog'
import { ApiError } from '../hooks/apiClient'
// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { ScriptDefinition } from '../types/scripts'
import RealScriptSecretBindingsDialog from './ScriptSecretBindingsDialog'
import type { ComponentProps } from 'react'
function ScriptSecretBindingsDialog(props: ComponentProps<typeof RealScriptSecretBindingsDialog>) { return <RealScriptSecretBindingsDialog {...props} authoringBaseline={props.authoringBaseline === undefined ? authoringFixture(props.script, props.scriptId, props.projectId) : props.authoringBaseline} /> }

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const apiMock = vi.hoisted(() => ({
  getScriptSecretStatus: vi.fn(),
  getScriptAuthoring: vi.fn(),
  updateScript: vi.fn(),
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

  it('renders zero-consumer null lists without hiding the secret recovery dialog', async () => {
    apiMock.getProjectSecretUsage.mockResolvedValueOnce({ project_id: 'project-1', name: 'LOGIN_TOKEN', usage_revision: 'empty-consumers', consumer_count: 0, reference_count: 0, consumers: null, limit: 50, offset: 0, has_more: false })
    await act(async () => { root.render(<ScriptSecretBindingsDialog scriptId="script-1" script={script} projectId="project-1" scriptName="Checkout Flow" onClose={vi.fn()} onRebound={vi.fn()} />) })
    await flush()
    expect(document.body.querySelector('.script-secret-bindings-dialog')!.textContent).toContain('0 consuming scripts')
    expect(document.body.textContent).not.toContain('Consuming scripts')
    expect(document.body.querySelector<HTMLSelectElement>('.script-secret-rebind-control select')!.disabled).toBe(false)
  })

  it('refuses rebind controls without a protected authoring pair', async () => {
    await act(async () => { root.render(<ScriptSecretBindingsDialog scriptId="script-1" script={script} projectId="project-1" scriptName="Checkout Flow" authoringBaseline={null} onClose={vi.fn()} onRebound={vi.fn()} />) })
    await flush()
    expect(document.body.querySelector<HTMLSelectElement>('.script-secret-rebind-control select')!.disabled).toBe(true)
    const button = Array.from(document.body.querySelectorAll<HTMLButtonElement>('button')).find(item => item.textContent === 'Rebind and share')!
    expect(button.disabled).toBe(true)
    await act(async () => { button.click() })
    expect(apiMock.rebindScriptSecretReference).not.toHaveBeenCalled()
  })

  it('creates a missing value for the exact read-only script reference', async () => {
    const onConfigured = vi.fn()
    await act(async () => {
      root.render(<ScriptSecretBindingsDialog scriptId="script-1" script={script} projectId="project-1" scriptName="Checkout Flow" onClose={vi.fn()} onConfigured={onConfigured} onRebound={vi.fn()} />)
    })
    await flush()

    const internalReference = document.body.querySelector<HTMLDetailsElement>('.secret-binding-internal-reference')
    expect(internalReference?.open).toBe(false)
    expect(internalReference?.querySelector('summary')?.textContent).toBe('Internal script reference')
    expect(internalReference?.querySelector('code')?.textContent).toBe('{{secret.LOGIN_TOKEN}}')
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
      metadata: { import: { source: 'bruno', preserved: { native_importer: 'apexload-bruno-native-importer' } } },
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
    expect(rows.map(row => row.querySelector('.secret-binding-internal-reference code')?.textContent)).toEqual([
      `{{secret.${passwordName}}}`,
      `{{secret.${usernameName}}}`,
    ])
    expect(rows.every(row => row.querySelector<HTMLDetailsElement>('.secret-binding-internal-reference')?.open === false)).toBe(true)

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

  it('keeps colliding friendly names visibly distinct', async () => {
    const firstName = 'CLIENT_ID_111111111111111111111111'
    const secondName = 'CLIENT_ID_222222222222222222222222'
    const importedScript: ScriptDefinition = {
      ...script,
      metadata: { import: { source: 'bruno', preserved: { native_importer: 'apexload-bruno-native-importer' } } },
      actions: [{
        name: 'Login',
        type: 'request',
        target: `/?first={{secret.${firstName}}}&second={{secret.${secondName}}}`,
      }],
    }

    await act(async () => {
      root.render(<ScriptSecretBindingsDialog scriptId="script-1" script={importedScript} projectId="project-1" scriptName="Case-sensitive variables" onClose={vi.fn()} onRebound={vi.fn()} />)
    })
    await flush()

    const rows = Array.from(document.body.querySelectorAll<HTMLElement>('.script-secret-binding-row'))
    expect(rows.map(row => row.querySelector('.script-secret-binding-summary strong')?.textContent)).toEqual([
      'CLIENT_ID · source 1',
      'CLIENT_ID · source 2',
    ])
    expect(rows.map(row => row.querySelector('.secret-binding-internal-reference code')?.textContent)).toEqual([
      `{{secret.${firstName}}}`,
      `{{secret.${secondName}}}`,
    ])
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

    expect(apiMock.rebindScriptSecretReference).toHaveBeenCalledWith(script, 'LOGIN_TOKEN', 'SHARED_LOGIN', authoringFixture(script))
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
    expect(apiMock.rebindScriptSecretReference).toHaveBeenCalledWith(script, 'LOGIN_TOKEN', 'LOGIN_TOKEN_CHECKOUT_FLOW', authoringFixture(script))
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
  it('refuses a late rebind callback after the dialog changes resource ownership', async () => {
    let complete!: (response: unknown) => void
    apiMock.rebindScriptSecretReference.mockReturnValueOnce(new Promise(resolve => { complete = resolve }))
    const onRebound = vi.fn()
    await act(async () => { root.render(<ScriptSecretBindingsDialog scriptId="script-1" script={script} projectId="project-1" scriptName="Checkout Flow" onClose={vi.fn()} onRebound={onRebound} />) })
    await flush()
    const select = document.body.querySelector<HTMLSelectElement>('select')!
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value')!.set!.call(select, 'SHARED_LOGIN')
      select.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await act(async () => { Array.from(document.body.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Rebind and share')!.click() })
    expect(apiMock.rebindScriptSecretReference).toHaveBeenCalledTimes(1)
    await act(async () => { root.render(<ScriptSecretBindingsDialog scriptId="script-2" script={{ ...script, id: 'script-2', project_id: 'project-2' }} projectId="project-2" scriptName="Other script" onClose={vi.fn()} onRebound={onRebound} />) })
    await act(async () => { complete({ script, changed_locations: ['/actions/0/headers/Authorization'], changed_count: 1 }) })
    expect(onRebound).not.toHaveBeenCalled()
  })

  it('keeps protected rebind buttons disabled during save reconciliation', async () => {
    await act(async () => { root.render(<ScriptSecretBindingsDialog scriptId="script-1" script={script} projectId="project-1" scriptName="Checkout Flow" rebindDisabled onClose={vi.fn()} onRebound={vi.fn()} />) })
    await flush()
    const select = document.body.querySelector<HTMLSelectElement>('select')!
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value')!.set!.call(select, 'SHARED_LOGIN')
      select.dispatchEvent(new Event('change', { bubbles: true }))
    })
    expect(Array.from(document.body.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Rebind and share')!.disabled).toBe(true)
    expect(apiMock.rebindScriptSecretReference).not.toHaveBeenCalled()
  })

  it('recovers the real binding child after conflict and same-placement reload', async () => {
    const pair = authoringFixture(script)
    apiMock.getScriptAuthoring.mockResolvedValueOnce(pair)
    apiMock.updateScript.mockRejectedValueOnce(new ApiError('stale', 412))
    await act(async () => { root.render(<StoredScriptSecretBindingsDialog stored={savedAuthoringFixture(script)} projectId="project-1" onClose={vi.fn()} onConfigured={vi.fn()} onSaved={vi.fn()} />) })
    await flush()
    const select = () => document.body.querySelector<HTMLSelectElement>('.script-secret-bindings-dialog select')!
    const choose = async () => { await act(async () => { Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value')!.set!.call(select(), 'SHARED_LOGIN'); select().dispatchEvent(new Event('change', { bubbles: true })) }) }
    const button = (name: string) => Array.from(document.body.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === name)!
    await choose()
    await act(async () => { button('Rebind and share').click() })
    await flush()
    expect(document.body.textContent).toContain('rebound draft is retained')
    apiMock.getScriptAuthoring.mockResolvedValueOnce(authoringFixture(script, 'script-1', 'project-1', 'c'))
    await act(async () => { button('Load current baseline').click() })
    await flush()
    await act(async () => { button('Reload script and discard draft').click() })
    await flush()
    await choose()
    expect(button('Rebind and share').disabled).toBe(false)
    expect(apiMock.updateScript).toHaveBeenCalledTimes(1)
  })

})
