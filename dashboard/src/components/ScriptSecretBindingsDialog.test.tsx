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
  getApplicationSecretStatuses: vi.fn(),
  listApplicationSecrets: vi.fn(),
  getApplicationSecretUsage: vi.fn(),
  createApplicationSecrets: vi.fn(),
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

  describe('create a new secret from the popup', () => {
    const button = (label: string, scope: ParentNode = document.body) => Array.from(scope.querySelectorAll<HTMLButtonElement>('button')).find(item => item.textContent === label)
    const row = (name: string) => Array.from(document.body.querySelectorAll<HTMLElement>('.script-secret-binding-row'))
      .find(item => item.querySelector('.script-secret-binding-summary strong')?.textContent === name)!
    const chooseCreate = async (scope: HTMLElement) => {
      const radio = Array.from(scope.querySelectorAll<HTMLInputElement>('.script-secret-bind-mode input[type="radio"]'))
        .find(input => input.nextElementSibling?.textContent === 'Create new secret')!
      await act(async () => { radio.click() })
    }
    const createControl = (scope: HTMLElement) => scope.querySelector<HTMLElement>('.script-secret-create-control')!
    const nameInput = (scope: HTMLElement) => createControl(scope).querySelector<HTMLInputElement>('input[type="text"]')!
    const valueInput = (scope: HTMLElement) => createControl(scope).querySelector<HTMLInputElement>('input[type="password"]')!
    const configuredLoginToken = () => apiMock.getProjectSecretStatuses.mockImplementation(async (_projectId: string, names: string[]) => ({
      project_id: 'project-1',
      capabilities: { provider: 'database', writable: true, create_supported: true, replace_supported: true },
      secrets: names.map(name => ({ name, configured: name === 'LOGIN_TOKEN' })),
    }))

    it('creates a suggested HTTP header secret, binds it, and drops the value', async () => {
      configuredLoginToken()
      const onRebound = vi.fn()
      const onClose = vi.fn()
      await act(async () => { root.render(<ScriptSecretBindingsDialog scriptId="script-1" script={script} projectId="project-1" scriptName="Checkout Flow" onClose={onClose} onRebound={onRebound} />) })
      await flush()
      const login = row('LOGIN_TOKEN')
      await chooseCreate(login)

      expect(nameInput(login).value).toBe('LOGIN_TOKEN_2')
      expect(document.body.querySelector(`label[for="${valueInput(login).id}"]`)?.textContent).toBe('Secret value for LOGIN_TOKEN')
      expect(document.body.querySelector(`label[for="${nameInput(login).id}"]`)?.textContent).toBe('New project secret name for LOGIN_TOKEN')
      expect(valueInput(login).type).toBe('password')
      expect(valueInput(login).autocomplete).toBe('new-password')
      act(() => setInput(valueInput(login), 'header-token-value'))
      await act(async () => { button('Create and bind', login)!.click() })
      await flush()

      expect(apiMock.createProjectSecrets).toHaveBeenCalledWith('project-1', [{
        name: 'LOGIN_TOKEN_2',
        value: 'header-token-value',
        expected_usage: { usage_revision: 'revision-LOGIN_TOKEN_2' },
      }])
      expect(apiMock.rebindScriptSecretReference).toHaveBeenCalledWith(script, 'LOGIN_TOKEN', 'LOGIN_TOKEN_2', expect.anything())
      expect(apiMock.createProjectSecrets.mock.invocationCallOrder[0]).toBeLessThan(apiMock.rebindScriptSecretReference.mock.invocationCallOrder[0])
      expect(onRebound).toHaveBeenCalledWith(expect.objectContaining({ name: 'Checkout Flow' }), ['/actions/0/headers/Authorization'], { createdSecretName: 'LOGIN_TOKEN_2' })
      expect(onClose).toHaveBeenCalled()
      expect(document.body.textContent).not.toContain('header-token-value')
      expect(Array.from(document.body.querySelectorAll<HTMLInputElement>('input')).some(input => input.value === 'header-token-value')).toBe(false)
    })

    it('creates and binds a Kafka OAuth client secret under an operator-chosen name', async () => {
      const kafkaScript: ScriptDefinition = {
        ...script,
        protocol: 'kafka',
        config: { kafka: { brokers: ['broker:9093'], oauth: { token_url: 'https://idp.test/token', client_id: '{{secret.KAFKA_CLIENT_ID}}', client_secret_ref: 'KAFKA_CLIENT_SECRET' } } } as ScriptDefinition['config'],
        actions: [{ name: 'Publish', type: 'kafka_produce', target: 'orders' }],
      }
      apiMock.rebindScriptSecretReference.mockImplementation(async (_script: ScriptDefinition, oldName: string, newName: string) => ({
        script: kafkaScript, old_name: oldName, new_name: newName, changed_locations: ['/config/kafka/oauth/client_secret_ref'], changed_count: 1,
      }))
      const onRebound = vi.fn()
      await act(async () => { root.render(<ScriptSecretBindingsDialog scriptId="script-1" script={kafkaScript} projectId="project-1" scriptName="Orders Producer" onClose={vi.fn()} onRebound={onRebound} />) })
      await flush()

      const rows = Array.from(document.body.querySelectorAll<HTMLElement>('.script-secret-binding-row'))
      expect(rows.map(item => item.querySelector('.script-secret-binding-summary strong')?.textContent)).toEqual(['KAFKA_CLIENT_ID', 'KAFKA_CLIENT_SECRET'])
      const clientSecret = row('KAFKA_CLIENT_SECRET')
      await chooseCreate(clientSecret)
      // The reference has no value and only this script uses it, so it keeps its own name.
      expect(nameInput(clientSecret).value).toBe('KAFKA_CLIENT_SECRET')
      act(() => {
        setInput(nameInput(clientSecret), 'orders-oauth.secret')
        setInput(valueInput(clientSecret), 'kafka-client-secret-value')
      })
      expect(createControl(clientSecret).querySelector('.secret-binding-review-reference code')?.textContent).toBe('{{secret.ORDERS_OAUTH_SECRET}}')
      await act(async () => { button('Create and bind', clientSecret)!.click() })
      await flush()

      expect(apiMock.createProjectSecrets).toHaveBeenCalledWith('project-1', [{
        name: 'ORDERS_OAUTH_SECRET',
        value: 'kafka-client-secret-value',
        expected_usage: { usage_revision: 'revision-ORDERS_OAUTH_SECRET' },
      }])
      expect(apiMock.rebindScriptSecretReference).toHaveBeenCalledWith(kafkaScript, 'KAFKA_CLIENT_SECRET', 'ORDERS_OAUTH_SECRET', expect.anything())
      expect(onRebound).toHaveBeenCalledWith(kafkaScript, ['/config/kafka/oauth/client_secret_ref'], { createdSecretName: 'ORDERS_OAUTH_SECRET' })
      expect(document.body.textContent).not.toContain('kafka-client-secret-value')
    })

    it('creates an Event Hubs connection string for its own reference without a rebind', async () => {
      const eventHubsScript: ScriptDefinition = {
        ...script,
        protocol: 'eventhubs',
        config: { eventhubs: { namespace: 'orders.servicebus.windows.net', event_hub: 'orders', connection_string_secret: '{{secret.EVENTHUBS_CONNECTION}}' } } as ScriptDefinition['config'],
        actions: [{ name: 'Send', type: 'eventhubs_send', target: 'orders' }],
      }
      apiMock.getProjectSecretUsage.mockImplementation(async (_projectId: string, name: string) => ({
        project_id: 'project-1', name, usage_revision: `revision-${name}`, consumer_count: 1, reference_count: 1,
        consumers: [{ script_id: 'script-1', script_name: 'Orders Sender', reference_count: 1 }],
        limit: 50, offset: 0, has_more: false,
      }))
      const onConfigured = vi.fn()
      await act(async () => { root.render(<ScriptSecretBindingsDialog scriptId="script-1" script={eventHubsScript} projectId="project-1" scriptName="Orders Sender" onClose={vi.fn()} onConfigured={onConfigured} onRebound={vi.fn()} />) })
      await flush()
      const connection = row('EVENTHUBS_CONNECTION')
      await chooseCreate(connection)
      expect(nameInput(connection).value).toBe('EVENTHUBS_CONNECTION')
      act(() => setInput(valueInput(connection), 'Endpoint=sb://orders/;SharedAccessKey=abc'))
      await act(async () => { button('Create value', connection)!.click() })
      await flush()

      expect(apiMock.createProjectSecrets).toHaveBeenCalledWith('project-1', [{
        name: 'EVENTHUBS_CONNECTION',
        value: 'Endpoint=sb://orders/;SharedAccessKey=abc',
        expected_usage: { usage_revision: 'revision-EVENTHUBS_CONNECTION', exclusive_script_id: 'script-1' },
      }])
      expect(apiMock.rebindScriptSecretReference).not.toHaveBeenCalled()
      expect(onConfigured).toHaveBeenCalled()
      expect(document.body.textContent).toContain('EVENTHUBS_CONNECTION was created in the project vault')
      expect(document.body.textContent).not.toContain('SharedAccessKey=abc')
      // The typed value is dropped, not kept for the next create.
      await chooseCreate(row('EVENTHUBS_CONNECTION'))
      expect(valueInput(row('EVENTHUBS_CONNECTION')).value).toBe('')
    })

    it('refuses a name already in the vault before calling the API', async () => {
      configuredLoginToken()
      await act(async () => { root.render(<ScriptSecretBindingsDialog scriptId="script-1" script={script} projectId="project-1" scriptName="Checkout Flow" onClose={vi.fn()} onRebound={vi.fn()} />) })
      await flush()
      const login = row('LOGIN_TOKEN')
      await chooseCreate(login)
      act(() => {
        setInput(nameInput(login), 'shared_login')
        setInput(valueInput(login), 'value')
      })
      expect(createControl(login).textContent).toContain('A project secret named SHARED_LOGIN already exists')
      expect(nameInput(login).getAttribute('aria-invalid')).toBe('true')
      expect(button('Create and bind', login)!.disabled).toBe(true)
      expect(apiMock.createProjectSecrets).not.toHaveBeenCalled()
    })

    it('refuses a name another reference in this script uses, and one other scripts reference', async () => {
      configuredLoginToken()
      const twoReferences: ScriptDefinition = {
        ...script,
        actions: [{ ...script.actions[0], headers: { Authorization: 'Bearer {{secret.LOGIN_TOKEN}}', 'X-Api-Key': '{{secret.API_KEY}}' } }],
      }
      apiMock.getProjectSecretUsage.mockImplementation(async (_projectId: string, name: string) => ({
        project_id: 'project-1', name, usage_revision: `revision-${name}`, consumer_count: name === 'ORDERS_TOKEN' ? 2 : 0, reference_count: 0,
        consumers: [], limit: 50, offset: 0, has_more: false,
      }))
      await act(async () => { root.render(<ScriptSecretBindingsDialog scriptId="script-1" script={twoReferences} projectId="project-1" scriptName="Checkout Flow" onClose={vi.fn()} onRebound={vi.fn()} />) })
      await flush()
      const login = row('LOGIN_TOKEN')
      await chooseCreate(login)
      act(() => {
        setInput(nameInput(login), 'API_KEY')
        setInput(valueInput(login), 'value')
      })
      expect(createControl(login).textContent).toContain('This script already uses API_KEY for another reference')
      expect(button('Create and bind', login)!.disabled).toBe(true)

      act(() => setInput(nameInput(login), 'ORDERS_TOKEN'))
      await act(async () => { button('Create and bind', login)!.click() })
      await flush()
      expect(document.body.querySelector('[role="alert"]')?.textContent).toContain('ORDERS_TOKEN is already referenced by 2 saved scripts')
      expect(apiMock.createProjectSecrets).not.toHaveBeenCalled()
      expect(apiMock.rebindScriptSecretReference).not.toHaveBeenCalled()
    })

    it.each([
      [new ApiError('project secret "LOGIN_TOKEN_2" already exists', 409), 'A project secret named LOGIN_TOKEN_2 already exists'],
      // The controller redacts 5xx detail to this text, with a log reference.
      // A 503 may follow a committed write, so it never claims the value was not stored.
      [new ApiError('internal server error', 503, { error: 'internal server error', ref: 'f9454308b5333b1b' }), 'did not confirm that LOGIN_TOKEN_2 was created. It may be unavailable, or the value may have been stored. The bindings are being refreshed: if LOGIN_TOKEN_2 then appears under "Use existing secret", select it instead of creating it again. Reference: f9454308b5333b1b.'],
      [new ApiError('forbidden', 403), 'You do not have permission to create project secrets'],
    ])('explains a refused create (%s)', async (failure, expected) => {
      configuredLoginToken()
      apiMock.createProjectSecrets.mockRejectedValueOnce(failure)
      await act(async () => { root.render(<ScriptSecretBindingsDialog scriptId="script-1" script={script} projectId="project-1" scriptName="Checkout Flow" onClose={vi.fn()} onRebound={vi.fn()} />) })
      await flush()
      const login = row('LOGIN_TOKEN')
      await chooseCreate(login)
      act(() => setInput(valueInput(login), 'never-shown'))
      await act(async () => { button('Create and bind', login)!.click() })
      await flush()

      expect(document.body.querySelector('[role="alert"]')?.textContent).toContain(expected)
      expect(document.body.textContent).not.toContain('never-shown')
      expect(apiMock.rebindScriptSecretReference).not.toHaveBeenCalled()
      if ((failure as ApiError).status !== 403) {
        // The list is reloaded so an existing or just-stored name is offered.
        expect(apiMock.listProjectSecrets).toHaveBeenCalledTimes(2)
      }
      if ((failure as ApiError).status === 403) {
        expect(Array.from(document.body.querySelectorAll<HTMLInputElement>('input')).some(input => input.value === 'never-shown')).toBe(false)
        // Binding still works; creating is no longer offered.
        expect(document.body.querySelector('.script-secret-bind-mode')).toBeNull()
        expect(document.body.querySelector('.script-secret-create-control')).toBeNull()
        expect(document.body.querySelector<HTMLSelectElement>('.script-secret-rebind-control select')).not.toBeNull()
      }
    })

    it('numbers past a name another script consumes and asks before sharing it', async () => {
      apiMock.getProjectSecretUsage.mockImplementation(async (_projectId: string, name: string) => ({
        project_id: 'project-1', name, usage_revision: `revision-${name}`, consumer_count: name === 'LOGIN_TOKEN' ? 1 : 0,
        reference_count: name === 'LOGIN_TOKEN' ? 1 : 0,
        consumers: name === 'LOGIN_TOKEN' ? [{ script_id: 'script-2', script_name: 'Refunds', reference_count: 1 }] : [],
        limit: 50, offset: 0, has_more: false,
      }))
      await act(async () => { root.render(<ScriptSecretBindingsDialog scriptId="script-1" script={script} projectId="project-1" scriptName="Checkout Flow" onClose={vi.fn()} onRebound={vi.fn()} />) })
      await flush()
      const login = row('LOGIN_TOKEN')
      await chooseCreate(login)
      // Only another script consumes LOGIN_TOKEN, so it is not offered as this script's own name.
      expect(nameInput(login).value).toBe('LOGIN_TOKEN_2')

      act(() => {
        setInput(nameInput(login), 'LOGIN_TOKEN')
        setInput(valueInput(login), 'shared-value')
      })
      const acknowledgement = createControl(login).querySelector<HTMLInputElement>('.script-secret-impact-acknowledgement input')!
      expect(acknowledgement).not.toBeNull()
      expect(button('Create value', login)!.disabled).toBe(true)
      await act(async () => { acknowledgement.click() })
      await act(async () => { button('Create value', login)!.click() })
      await flush()

      expect(apiMock.createProjectSecrets).toHaveBeenCalledWith('project-1', [{
        name: 'LOGIN_TOKEN',
        value: 'shared-value',
        expected_usage: { usage_revision: 'revision-LOGIN_TOKEN' },
      }])
      expect(apiMock.rebindScriptSecretReference).not.toHaveBeenCalled()
    })

    it('offers a created value in the picker when the rebind then fails', async () => {
      configuredLoginToken()
      apiMock.listProjectSecrets
        .mockResolvedValueOnce({
          project_id: 'project-1',
          capabilities: { provider: 'database', writable: true, create_supported: true, replace_supported: true },
          secrets: [{ name: 'SHARED_LOGIN', provider: 'database', writable: true }],
          count: 1,
        })
        .mockResolvedValue({
          project_id: 'project-1',
          capabilities: { provider: 'database', writable: true, create_supported: true, replace_supported: true },
          secrets: [{ name: 'LOGIN_TOKEN_2', provider: 'database', writable: true }, { name: 'SHARED_LOGIN', provider: 'database', writable: true }],
          count: 2,
        })
      apiMock.rebindScriptSecretReference.mockRejectedValueOnce(new ApiError('script authored content changed', 412))
      const onRebound = vi.fn()
      await act(async () => { root.render(<ScriptSecretBindingsDialog scriptId="script-1" script={script} projectId="project-1" scriptName="Checkout Flow" onClose={vi.fn()} onRebound={onRebound} />) })
      await flush()
      const login = row('LOGIN_TOKEN')
      await chooseCreate(login)
      act(() => setInput(valueInput(login), 'stored-once'))
      await act(async () => { button('Create and bind', login)!.click() })
      await flush()

      expect(document.body.querySelector('[role="alert"]')?.textContent).toContain('LOGIN_TOKEN_2 was created, but the script rebind did not complete. It is selected under "Use existing secret"')
      const picker = row('LOGIN_TOKEN').querySelector<HTMLSelectElement>('.script-secret-rebind-control select')!
      expect(picker.value).toBe('LOGIN_TOKEN_2')
      expect(row('LOGIN_TOKEN').querySelector('.script-secret-create-control')).toBeNull()
      expect(Array.from(document.body.querySelectorAll<HTMLInputElement>('input')).some(input => input.value === 'stored-once')).toBe(false)
      expect(apiMock.createProjectSecrets).toHaveBeenCalledTimes(1)
      // The typed value was dropped with the create, not kept for a second one.
      await chooseCreate(row('LOGIN_TOKEN'))
      expect(valueInput(row('LOGIN_TOKEN')).value).toBe('')
      const existing = Array.from(row('LOGIN_TOKEN').querySelectorAll<HTMLInputElement>('.script-secret-bind-mode input[type="radio"]'))
        .find(input => input.nextElementSibling?.textContent === 'Use existing secret')!
      await act(async () => { existing.click() })
      expect(row('LOGIN_TOKEN').querySelector<HTMLSelectElement>('.script-secret-rebind-control select')!.value).toBe('LOGIN_TOKEN_2')
      // Binding the created value later still reports it as created by this dialog.
      await act(async () => { button('Rebind and share', row('LOGIN_TOKEN'))!.click() })
      await flush()
      expect(onRebound).toHaveBeenCalledWith(expect.anything(), ['/actions/0/headers/Authorization'], { createdSecretName: 'LOGIN_TOKEN_2' })
    })

    it('keeps the store failure on screen when the refresh after it also fails', async () => {
      configuredLoginToken()
      apiMock.listProjectSecrets
        .mockResolvedValueOnce({
          project_id: 'project-1',
          capabilities: { provider: 'database', writable: true, create_supported: true, replace_supported: true },
          secrets: [], count: 0,
        })
        .mockRejectedValue(new ApiError('project secret metadata is unavailable', 503))
      apiMock.createProjectSecrets.mockRejectedValueOnce(new ApiError('internal server error', 503))
      await act(async () => { root.render(<ScriptSecretBindingsDialog scriptId="script-1" script={script} projectId="project-1" scriptName="Checkout Flow" onClose={vi.fn()} onRebound={vi.fn()} />) })
      await flush()
      const login = row('LOGIN_TOKEN')
      await chooseCreate(login)
      act(() => setInput(valueInput(login), 'unconfirmed'))
      await act(async () => { button('Create and bind', login)!.click() })
      await flush()
      await flush()
      expect(document.body.querySelector('[role="alert"]')?.textContent).toContain('did not confirm that LOGIN_TOKEN_2 was created')
    })

    it('refuses the reserved runtime-override namespace before calling the API', async () => {
      configuredLoginToken()
      await act(async () => { root.render(<ScriptSecretBindingsDialog scriptId="script-1" script={script} projectId="project-1" scriptName="Checkout Flow" onClose={vi.fn()} onRebound={vi.fn()} />) })
      await flush()
      const login = row('LOGIN_TOKEN')
      await chooseCreate(login)
      act(() => {
        setInput(nameInput(login), 'apex_runtime_override_token')
        setInput(valueInput(login), 'never-sent')
      })
      expect(createControl(login).textContent).toContain('Names starting with APEX_RUNTIME_OVERRIDE_ are reserved for runtime overrides.')
      expect(button('Create and bind', login)!.disabled).toBe(true)
      expect(apiMock.createProjectSecrets).not.toHaveBeenCalled()
    })

    it('names the vault a colliding name lives in for an application-placed script', async () => {
      const capabilities = { provider: 'database', writable: true, create_supported: true, replace_supported: true }
      apiMock.getApplicationSecretStatuses.mockImplementation(async (_applicationId: string, names: string[]) => ({
        application_id: 'app-1', capabilities, secrets: names.map(name => ({ name, configured: name === 'LOGIN_TOKEN' })),
      }))
      apiMock.listApplicationSecrets.mockResolvedValue({ application_id: 'app-1', capabilities, secrets: [{ name: 'APP_TOKEN', provider: 'database', writable: true }], count: 1 })
      apiMock.getApplicationSecretUsage.mockImplementation(async (_applicationId: string, name: string) => ({
        application_id: 'app-1', name, usage_revision: `revision-${name}`, consumer_count: 0, reference_count: 0, consumers: [], limit: 50, offset: 0, has_more: false,
      }))
      await act(async () => { root.render(<ScriptSecretBindingsDialog scriptId="script-1" script={script} projectId="project-1" applicationId="app-1" scriptName="Checkout Flow" onClose={vi.fn()} onRebound={vi.fn()} />) })
      await flush()
      const login = row('LOGIN_TOKEN')
      await chooseCreate(login)
      act(() => setInput(nameInput(login), 'SHARED_LOGIN'))
      expect(createControl(login).textContent).toContain('A project secret named SHARED_LOGIN already exists, and an application secret of that name would shadow it.')
      act(() => setInput(nameInput(login), 'APP_TOKEN'))
      expect(createControl(login).textContent).toContain('An application secret named APP_TOKEN already exists.')
    })

    it('reloads and asks for the share confirmation when another script starts using the name', async () => {
      let loginUsageReads = 0
      apiMock.getProjectSecretUsage.mockImplementation(async (_projectId: string, name: string) => {
        const later = name === 'LOGIN_TOKEN' && ++loginUsageReads > 1
        const consumers = name !== 'LOGIN_TOKEN' ? [] : [
          { script_id: 'script-1', script_name: 'Checkout Flow', reference_count: 1 },
          ...(later ? [{ script_id: 'script-2', script_name: 'Refunds', reference_count: 1 }] : []),
        ]
        return { project_id: 'project-1', name, usage_revision: `revision-${name}-${loginUsageReads}`, consumer_count: consumers.length, reference_count: consumers.length, consumers, limit: 50, offset: 0, has_more: false }
      })
      await act(async () => { root.render(<ScriptSecretBindingsDialog scriptId="script-1" script={script} projectId="project-1" scriptName="Checkout Flow" onClose={vi.fn()} onRebound={vi.fn()} />) })
      await flush()
      const login = row('LOGIN_TOKEN')
      await chooseCreate(login)
      expect(nameInput(login).value).toBe('LOGIN_TOKEN')
      expect(createControl(login).querySelector('.script-secret-impact-acknowledgement')).toBeNull()
      act(() => setInput(valueInput(login), 'shared-later'))
      await act(async () => { button('Create value', login)!.click() })
      await flush()

      expect(document.body.querySelector('[role="alert"]')?.textContent).toContain('Another saved script now references this name.')
      expect(apiMock.createProjectSecrets).not.toHaveBeenCalled()
      expect(createControl(row('LOGIN_TOKEN')).querySelector('.script-secret-impact-acknowledgement input')).not.toBeNull()
    })

    it('stops suggesting a name a usage check found referenced elsewhere', async () => {
      configuredLoginToken()
      apiMock.getProjectSecretUsage.mockImplementation(async (_projectId: string, name: string) => ({
        project_id: 'project-1', name, usage_revision: `revision-${name}`,
        consumer_count: name === 'LOGIN_TOKEN_2' ? 1 : 0, reference_count: name === 'LOGIN_TOKEN_2' ? 1 : 0,
        consumers: name === 'LOGIN_TOKEN_2' ? [{ script_id: 'script-2', script_name: 'Refunds', reference_count: 1 }] : [],
        limit: 50, offset: 0, has_more: false,
      }))
      await act(async () => { root.render(<ScriptSecretBindingsDialog scriptId="script-1" script={script} projectId="project-1" scriptName="Checkout Flow" onClose={vi.fn()} onRebound={vi.fn()} />) })
      await flush()
      const login = row('LOGIN_TOKEN')
      await chooseCreate(login)
      expect(nameInput(login).value).toBe('LOGIN_TOKEN_2')
      act(() => setInput(valueInput(login), 'not-shared'))
      await act(async () => { button('Create and bind', login)!.click() })
      await flush()

      expect(document.body.querySelector('[role="alert"]')?.textContent).toContain('LOGIN_TOKEN_2 is already referenced by 1 saved script')
      expect(nameInput(row('LOGIN_TOKEN')).value).toBe('LOGIN_TOKEN_3')
      act(() => setInput(nameInput(row('LOGIN_TOKEN')), 'LOGIN_TOKEN_2'))
      expect(createControl(row('LOGIN_TOKEN')).textContent).toContain('LOGIN_TOKEN_2 is already referenced by another saved script.')
      expect(apiMock.createProjectSecrets).not.toHaveBeenCalled()
    })

    it('says nothing was created when the pre-create check finds the store unavailable', async () => {
      configuredLoginToken()
      await act(async () => { root.render(<ScriptSecretBindingsDialog scriptId="script-1" script={script} projectId="project-1" scriptName="Checkout Flow" onClose={vi.fn()} onRebound={vi.fn()} />) })
      await flush()
      apiMock.getProjectSecretStatuses.mockRejectedValueOnce(new ApiError('internal server error', 503))
      const login = row('LOGIN_TOKEN')
      await chooseCreate(login)
      act(() => setInput(valueInput(login), 'never-sent'))
      await act(async () => { button('Create and bind', login)!.click() })
      await flush()
      expect(document.body.querySelector('[role="alert"]')?.textContent).toContain('The project secret store is unavailable, so nothing was created.')
      expect(apiMock.createProjectSecrets).not.toHaveBeenCalled()
    })

    it('marks an in-place create configured when the refresh after it fails', async () => {
      await act(async () => { root.render(<ScriptSecretBindingsDialog scriptId="script-1" script={script} projectId="project-1" scriptName="Checkout Flow" onClose={vi.fn()} onRebound={vi.fn()} />) })
      await flush()
      apiMock.listProjectSecrets.mockRejectedValue(new ApiError('project secret metadata is unavailable', 503))
      const login = row('LOGIN_TOKEN')
      await chooseCreate(login)
      expect(nameInput(login).value).toBe('LOGIN_TOKEN')
      act(() => setInput(valueInput(login), 'stored-in-place'))
      await act(async () => { button('Create value', login)!.click() })
      await flush()
      expect(document.body.querySelector('[role="alert"]')?.textContent).toContain('LOGIN_TOKEN was created and this script\'s reference uses it, but the bindings could not refresh.')
      expect(row('LOGIN_TOKEN').querySelector('.script-health-token')?.textContent).toBe('Configured')
      expect(row('LOGIN_TOKEN').querySelector('.script-secret-value-control')).toBeNull()
    })

    it('reloads and says the reference is configured when its own name gained a value', async () => {
      let statusReads = 0
      apiMock.getProjectSecretStatuses.mockImplementation(async (_projectId: string, names: string[]) => ({
        project_id: 'project-1',
        capabilities: { provider: 'database', writable: true, create_supported: true, replace_supported: true },
        secrets: names.map(name => ({ name, configured: name === 'LOGIN_TOKEN' && ++statusReads > 1 })),
      }))
      await act(async () => { root.render(<ScriptSecretBindingsDialog scriptId="script-1" script={script} projectId="project-1" scriptName="Checkout Flow" onClose={vi.fn()} onRebound={vi.fn()} />) })
      await flush()
      const login = row('LOGIN_TOKEN')
      await chooseCreate(login)
      act(() => setInput(valueInput(login), 'too-late'))
      await act(async () => { button('Create value', login)!.click() })
      await flush()
      expect(document.body.querySelector('[role="alert"]')?.textContent).toContain('LOGIN_TOKEN already has a value in the project vault.')
      expect(apiMock.listProjectSecrets).toHaveBeenCalledTimes(2)
      expect(apiMock.createProjectSecrets).not.toHaveBeenCalled()
    })

    it('refuses an application secret that would shadow a project secret found at create time', async () => {
      const capabilities = { provider: 'database', writable: true, create_supported: true, replace_supported: true }
      apiMock.getApplicationSecretStatuses.mockImplementation(async (_applicationId: string, names: string[]) => ({
        application_id: 'app-1', capabilities,
        secrets: names.map(name => name === 'LATE_PROJECT' ? { name, configured: true, source_scope: 'project' } : { name, configured: false }),
      }))
      apiMock.listApplicationSecrets.mockResolvedValue({ application_id: 'app-1', capabilities, secrets: [], count: 0 })
      apiMock.getApplicationSecretUsage.mockImplementation(async (_applicationId: string, name: string) => ({
        application_id: 'app-1', name, usage_revision: `revision-${name}`, consumer_count: 0, reference_count: 0, consumers: [], limit: 50, offset: 0, has_more: false,
      }))
      await act(async () => { root.render(<ScriptSecretBindingsDialog scriptId="script-1" script={script} projectId="project-1" applicationId="app-1" scriptName="Checkout Flow" onClose={vi.fn()} onRebound={vi.fn()} />) })
      await flush()
      const login = row('LOGIN_TOKEN')
      await chooseCreate(login)
      act(() => {
        setInput(nameInput(login), 'LATE_PROJECT')
        setInput(valueInput(login), 'never-sent')
      })
      await act(async () => { button('Create and bind', login)!.click() })
      await flush()
      expect(document.body.querySelector('[role="alert"]')?.textContent).toContain('A project secret named LATE_PROJECT already exists, and an application secret of that name would shadow it.')
      expect(apiMock.createApplicationSecrets).not.toHaveBeenCalled()
      expect(apiMock.listProjectSecrets).toHaveBeenCalledTimes(2)
    })

    it('offers only the existing picker when the secret store cannot create', async () => {
      apiMock.listProjectSecrets.mockResolvedValue({
        project_id: 'project-1',
        capabilities: { provider: 'unavailable', writable: false, create_supported: false, replace_supported: false },
        secrets: [{ name: 'SHARED_LOGIN', provider: 'env', writable: false }],
        count: 1,
      })
      await act(async () => { root.render(<ScriptSecretBindingsDialog scriptId="script-1" script={script} projectId="project-1" scriptName="Checkout Flow" onClose={vi.fn()} onRebound={vi.fn()} />) })
      await flush()
      expect(document.body.querySelector('.script-secret-bind-mode')).toBeNull()
      expect(document.body.querySelector('.script-secret-create-unavailable')?.textContent).toContain('no writable project secret store is configured')
      expect(document.body.querySelector('input[type="password"]')).toBeNull()
      // The exact reference stays visible without a value field.
      expect(document.body.querySelector('.secret-binding-internal-reference code')?.textContent).toBe('{{secret.LOGIN_TOKEN}}')
      expect(button('Configure exact reference')).toBeUndefined()
      expect(document.body.querySelector<HTMLSelectElement>('.script-secret-rebind-control select')).not.toBeNull()
    })

    it('offers only the existing picker to a session that cannot create secrets', async () => {
      await act(async () => { root.render(<ScriptSecretBindingsDialog scriptId="script-1" script={script} projectId="project-1" scriptName="Checkout Flow" canCreateSecrets={false} onClose={vi.fn()} onRebound={vi.fn()} />) })
      await flush()
      expect(document.body.querySelector('.script-secret-bind-mode')).toBeNull()
      expect(document.body.querySelector('.script-secret-create-unavailable')).toBeNull()
      expect(document.body.querySelector('input[type="password"]')).toBeNull()
      expect(button('Configure exact reference')).toBeUndefined()
      expect(document.body.querySelector<HTMLSelectElement>('.script-secret-rebind-control select')).not.toBeNull()
    })

    it('clears a typed value when switching back to the existing picker', async () => {
      configuredLoginToken()
      await act(async () => { root.render(<ScriptSecretBindingsDialog scriptId="script-1" script={script} projectId="project-1" scriptName="Checkout Flow" onClose={vi.fn()} onRebound={vi.fn()} />) })
      await flush()
      const login = row('LOGIN_TOKEN')
      await chooseCreate(login)
      act(() => setInput(valueInput(login), 'abandoned-value'))
      const existing = Array.from(login.querySelectorAll<HTMLInputElement>('.script-secret-bind-mode input[type="radio"]'))
        .find(input => input.nextElementSibling?.textContent === 'Use existing secret')!
      await act(async () => { existing.click() })
      await chooseCreate(login)
      expect(valueInput(login).value).toBe('')
    })
  })
})
