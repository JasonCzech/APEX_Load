// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import SharedImportValues from './SharedImportValues'
import { reachLabel, sharedValueNameError } from './sharedImportValueUtils'
import { type SharedValueCandidate, type SharedValues } from '../types/scriptImports'
import { type ScriptDefinition } from '../types/scripts'

const applyMock = vi.fn()
const listDataPoolsMock = vi.fn()
const listProjectSecretsMock = vi.fn()

vi.mock('../hooks/useApi', () => ({
  useApi: () => ({
    applyImportSharedValues: applyMock,
    listDataPools: listDataPoolsMock,
    listProjectSecrets: listProjectSecretsMock,
  }),
}))

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

function script(name: string): ScriptDefinition {
  return { name, version: '1.0', protocol: 'http', config: { base_url: 'https://stage.example.com' }, actions: [] }
}

const scripts = [script('get-users'), script('get-orders')]

function candidate(overrides: Partial<SharedValueCandidate>): SharedValueCandidate {
  return {
    id: 'sv_base',
    kind: 'existing_parameter',
    name: 'base_url',
    current_value: 'https://stage.example.com',
    occurrence_count: 2,
    script_names: ['get-users', 'get-orders'],
    rationale: 'Defined once per script by the importer.',
    ...overrides,
  }
}

function buildSharedValues(candidates: SharedValueCandidate[]): SharedValues {
  return {
    candidates,
    summary: {
      total: candidates.length,
      by_kind: {},
      credential_count: candidates.filter(c => c.credential_shaped).length,
      multi_script_count: candidates.filter(c => (c.script_names?.length ?? 0) > 1).length,
    },
  }
}

describe('SharedImportValues', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    applyMock.mockReset()
    listDataPoolsMock.mockReset()
    listDataPoolsMock.mockResolvedValue({ data_pools: [] })
    listProjectSecretsMock.mockReset()
    listProjectSecretsMock.mockResolvedValue({ secrets: [] })
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(() => {
    act(() => root.unmount())
    container.remove()
  })

  function render(sharedValues: SharedValues, extra: Record<string, unknown> = {}) {
    const onApplied = vi.fn()
    act(() => {
      root.render(
        <SharedImportValues
          sharedValues={sharedValues}
          scripts={scripts}
          onApplied={onApplied}
          {...extra}
        />,
      )
    })
    return onApplied
  }

  function inputFor(id: string): HTMLInputElement {
    const element = container.querySelector<HTMLInputElement>(`#shared-value-${id}`)
    if (!element) throw new Error(`no value input for ${id}`)
    return element
  }

  function type(element: HTMLInputElement, value: string) {
    act(() => {
      const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')?.set
      setter?.call(element, value)
      element.dispatchEvent(new Event('input', { bubbles: true }))
    })
  }

  it('separates required inputs from optional substitutions and retains entered values while searching', () => {
    render(buildSharedValues([
      candidate({ id: 'required', kind: 'source_variable', name: 'customer_id', current_value: '' }),
      candidate({ id: 'optional', name: 'base_url' }),
    ]), { groupRequirements: true })
    const groups = Array.from(container.querySelectorAll('details'))
    expect(groups[0].open).toBe(true)
    expect(groups[0].textContent).toContain('customer_id')
    expect(groups[0].textContent).not.toContain('base_url')
    expect(groups[1].open).toBe(false)
    expect(groups[1].textContent).toContain('base_url')
    type(inputFor('required'), 'customer-42')
    const search = container.querySelector<HTMLInputElement>('input[type=search]')!
    type(search, 'base_url')
    expect(container.querySelector('#shared-value-required')).toBeNull()
    type(search, '')
    expect(inputFor('required').value).toBe('customer-42')
  })

  function select(label: string, value: string) {
    const element = Array.from(container.querySelectorAll('label')).find(item => item.textContent?.startsWith(label))?.querySelector('select')
    if (!element) throw new Error(`no select labelled ${label}`)
    act(() => {
      element.value = value
      element.dispatchEvent(new Event('change', { bubbles: true }))
    })
  }

  function fieldIn(label: string): HTMLInputElement {
    const element = Array.from(container.querySelectorAll('label')).find(item => item.textContent?.startsWith(label))?.querySelector('input')
    if (!element) throw new Error(`no input labelled ${label}`)
    return element
  }

  // Any importer's unset input can be read per virtual user, as Bruno's can:
  // a data pool column, or a value generated each iteration. Only complete
  // bindings are sent, instead of a value for every user.
  it('supplies an unset input per virtual user from a data pool or a generator', async () => {
    applyMock.mockResolvedValue({ scripts, diffs: [], applied_count: 2 })
    render(buildSharedValues([
      candidate({ id: 'tenant', kind: 'source_variable', name: 'tenant', current_value: '' }),
      candidate({ id: 'request_id', kind: 'source_variable', name: 'request_id', current_value: '' }),
    ]))
    select('Supply tenant', 'data')
    type(fieldIn('Alias for tenant'), 'tenants')
    const button = container.querySelector<HTMLButtonElement>('.shared-values-actions button')!
    expect(button.disabled).toBe(true)
    type(fieldIn('Column for tenant'), 'id')
    select('Supply request_id', 'generated')
    expect(button.textContent).toBe('Apply to all scripts (2)')
    await act(async () => {
      button.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(applyMock).toHaveBeenCalledWith({
      scripts,
      values: {},
      bindings: {
        tenant: { mode: 'data', alias: 'tenants', column: 'id' },
        request_id: { mode: 'generated', generator: 'uuid' },
      },
    })
  })

  it('starts each unset input from how the scripts a re-import updates supplied it', async () => {
    applyMock.mockResolvedValue({ scripts, diffs: [], applied_count: 2 })
    render(buildSharedValues([
      candidate({ id: 'region', kind: 'source_variable', name: 'region', current_value: '' }),
      candidate({ id: 'tenant', kind: 'source_variable', name: 'tenant', current_value: '' }),
      candidate({ id: 'request_id', kind: 'source_variable', name: 'request_id', current_value: '' }),
    ]), {
      previousInputs: {
        region: { mode: 'value', value: 'us-east' },
        tenant: { mode: 'data', alias: 'tenants', column: 'id' },
        request_id: { mode: 'generated', generator: 'digits', digits: 6 },
      },
    })
    expect(inputFor('region').value).toBe('us-east')
    const button = container.querySelector<HTMLButtonElement>('.shared-values-actions button')!
    expect(button.textContent).toBe('Apply to all scripts (3)')
    await act(async () => {
      button.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(applyMock).toHaveBeenCalledWith({
      scripts,
      values: { region: 'us-east' },
      bindings: {
        tenant: { mode: 'data', alias: 'tenants', column: 'id' },
        request_id: { mode: 'generated', generator: 'digits', digits: 6 },
      },
    })
  })

  it('keeps typed inputs when the scripts a re-import updates change', () => {
    const sharedValues = buildSharedValues([
      candidate({ id: 'region', kind: 'source_variable', name: 'region', current_value: '' }),
      candidate({ id: 'tenant', kind: 'source_variable', name: 'tenant', current_value: '' }),
    ])
    render(sharedValues, {
      previousInputs: {
        region: { mode: 'value', value: 'us-east' },
        tenant: { mode: 'value', value: 'acme' },
      },
    })
    type(inputFor('tenant'), 'globex')
    // The operator switches the save to a copy: only the untouched input
    // loses its previous value.
    render(sharedValues, { previousInputs: undefined })
    expect(inputFor('region').value).toBe('')
    expect(inputFor('tenant').value).toBe('globex')
    render(sharedValues, { previousInputs: { region: { mode: 'value', value: 'eu-west' } } })
    expect(inputFor('region').value).toBe('eu-west')
    expect(inputFor('tenant').value).toBe('globex')
  })

  it('reads the data pools of its scripts\' project to offer them', async () => {
    listDataPoolsMock.mockResolvedValue({ data_pools: [{ id: 'pool-1', name: 'Tenants', columns: ['tenant_id'] }] })
    act(() => {
      root.render(
        <SharedImportValues
          sharedValues={buildSharedValues([candidate({ id: 'tenant', kind: 'source_variable', name: 'tenant', current_value: '' })])}
          scripts={[{ ...script('get-users'), project_id: 'project-1' }]}
          onApplied={vi.fn()}
        />,
      )
    })
    await act(async () => { await Promise.resolve() })
    expect(listDataPoolsMock).toHaveBeenCalledWith('project-1')
    select('Supply tenant', 'data')
    expect(container.textContent).toContain('Tenants')
  })

  it('pre-fills the value the importer already produced', () => {
    render(buildSharedValues([candidate({})]))
    expect(inputFor('sv_base').value).toBe('https://stage.example.com')
    expect(container.textContent).toContain('across 2 generated scripts')
  })

  // Rewriting every generated script is not something that should happen just
  // because the panel rendered.
  it('does not apply until the operator asks', () => {
    render(buildSharedValues([candidate({})]))
    expect(applyMock).not.toHaveBeenCalled()
    const button = container.querySelector<HTMLButtonElement>('.shared-values-actions button')
    expect(button?.disabled).toBe(true)
  })

  // Applying changes what the collection shares, so the server's re-derived
  // candidates must reach the caller — otherwise the panel keeps showing values
  // the scripts no longer hold.
  it('sends every script and the entered values, and forwards the refreshed candidates', async () => {
    const refreshed = buildSharedValues([candidate({ current_value: 'https://prod.example.com' })])
    applyMock.mockResolvedValue({ scripts, diffs: [], applied_count: 1, shared_values: refreshed })
    const onApplied = render(buildSharedValues([candidate({})]))

    type(inputFor('sv_base'), 'https://prod.example.com')
    const button = container.querySelector<HTMLButtonElement>('.shared-values-actions button')!
    expect(button.disabled).toBe(false)
    await act(async () => {
      button.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(applyMock).toHaveBeenCalledWith({
      scripts,
      values: { sv_base: 'https://prod.example.com' },
    })
    expect(onApplied).toHaveBeenCalledWith(scripts, [], refreshed, undefined)
  })

  // The apply endpoint rotates each credential-preserving draft's capability;
  // the caller needs those back or the drafts it still holds cannot be saved.
  it('forwards rotated draft expiries positionally', async () => {
    applyMock.mockResolvedValue({
      scripts,
      diffs: [],
      applied_count: 1,
      import_draft_expires_at: [null, '2026-08-19T20:00:00Z'],
    })
    const onApplied = render(buildSharedValues([candidate({})]))

    type(inputFor('sv_base'), 'https://prod.example.com')
    await act(async () => {
      container.querySelector<HTMLButtonElement>('.shared-values-actions button')!
        .dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(onApplied).toHaveBeenCalledWith(scripts, [], undefined, [null, '2026-08-19T20:00:00Z'])
  })

  // A parameter named like a credential is masked out of every later read, so
  // the panel must not offer to store one: a credential reads a project secret
  // or a data pool column, and only once the operator chooses one.
  it('supplies a credential-shaped input from a project secret the operator chooses, never a value', async () => {
    applyMock.mockResolvedValue({ scripts, diffs: [], applied_count: 1 })
    render(buildSharedValues([candidate({
      id: 'sv_token', name: 'apiToken', kind: 'source_variable',
      credential_shaped: true, current_value: undefined,
    })]))
    expect(container.querySelector('#shared-value-sv_token')).toBeNull()
    const supply = container.querySelector<HTMLSelectElement>('.import-input-supply select')!
    expect(Array.from(supply.options).map(option => option.value)).toEqual(['value', 'secret', 'data'])
    expect(supply.value).toBe('value')
    const button = () => container.querySelector<HTMLButtonElement>('.shared-values-actions button')!
    expect(button().disabled).toBe(true)

    select('Supply apiToken', 'secret')
    expect(container.textContent).toContain('{{secret.API_TOKEN}}')
    await act(async () => {
      button().dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(applyMock).toHaveBeenCalledWith({ scripts, values: {}, bindings: { sv_token: { mode: 'secret', secret: 'API_TOKEN' } } })
  })

  // Bruno's lesson: a credential never reads another credential's secret by
  // default, and a name the operator types that the project already holds is
  // called out. Dots and hyphens become underscores, as the secret store reads them.
  it('suggests a project secret no other credential uses and flags one that is', async () => {
    listProjectSecretsMock.mockResolvedValue({ secrets: [{ name: 'API_TOKEN' }, { name: 'ORDERS_KEY' }] })
    act(() => {
      root.render(
        <SharedImportValues
          sharedValues={buildSharedValues([candidate({ id: 'sv_token', name: 'apiToken', kind: 'source_variable', credential_shaped: true, current_value: undefined })])}
          scripts={[{ ...script('get-users'), project_id: 'project-1' }]}
          onApplied={vi.fn()}
        />,
      )
    })
    await act(async () => { await Promise.resolve() })
    expect(listProjectSecretsMock).toHaveBeenCalledWith('project-1')
    select('Supply apiToken', 'secret')
    expect(fieldIn('Project secret for apiToken').value).toBe('API_TOKEN_2')
    expect(container.textContent).not.toContain('The project already has')

    type(fieldIn('Project secret for apiToken'), 'orders.key')
    expect(fieldIn('Project secret for apiToken').value).toBe('ORDERS_KEY')
    expect(container.textContent).toContain('The project already has ORDERS_KEY')

    type(fieldIn('Project secret for apiToken'), 'orders key')
    expect(container.querySelector('.import-input-supply-error')?.textContent).toContain('capital letters, digits and underscores')
  })

  // Two credentials that would share a name read two secrets.
  it('numbers a second credential that wants a secret name another row chose', () => {
    render(buildSharedValues([
      candidate({ id: 'sv_token', name: 'apiToken', kind: 'source_variable', credential_shaped: true, current_value: undefined }),
      candidate({ id: 'sv_token_snake', name: 'api_token', kind: 'source_variable', credential_shaped: true, current_value: undefined }),
    ]))
    select('Supply apiToken', 'secret')
    select('Supply api_token', 'secret')
    expect(fieldIn('Project secret for apiToken').value).toBe('API_TOKEN')
    expect(fieldIn('Project secret for api_token').value).toBe('API_TOKEN_2')
  })

  // A re-import reads the secret the scripts it updates read, as the operator
  // chose it before.
  it('starts a credential from the project secret the updated scripts read', async () => {
    listProjectSecretsMock.mockResolvedValue({ secrets: [{ name: 'ORDERS_KEY' }] })
    render(buildSharedValues([candidate({ id: 'sv_token', name: 'apiToken', kind: 'source_variable', credential_shaped: true, current_value: undefined })]), {
      previousInputs: { apiToken: { mode: 'secret', reference: '{{secret.ORDERS_KEY}}' } },
      scripts: [{ ...script('get-users'), project_id: 'project-1' }],
    })
    await act(async () => { await Promise.resolve() })
    expect(listProjectSecretsMock).toHaveBeenCalledWith('project-1')
    expect(fieldIn('Project secret for apiToken').value).toBe('ORDERS_KEY')
    expect(container.textContent).toContain('As the last import of these scripts supplied it.')
    expect(container.textContent).not.toContain('The project already has')
  })

  // A collect-only panel has no supply control; it says where a credential is
  // set.
  it('points a credential-shaped candidate to the Credentials tab without a supply control', () => {
    render(buildSharedValues([candidate({
      id: 'sv_pw', name: 'password', kind: 'source_variable',
      credential_shaped: true, current_value: undefined,
    })]), { collectOnly: true })
    expect(container.querySelector('#shared-value-sv_pw')).toBeNull()
    expect(container.textContent).toContain('Credentials tab')
  })

  it('collect mode reports values instead of calling the endpoint', () => {
    const onValuesChange = vi.fn()
    render(buildSharedValues([candidate({})]), { collectOnly: true, scriptCount: 40, onValuesChange, scripts: [] })

    type(inputFor('sv_base'), 'https://prod.example.com')
    expect(applyMock).not.toHaveBeenCalled()
    expect(onValuesChange).toHaveBeenLastCalledWith({ sv_base: 'https://prod.example.com' })
    expect(container.textContent).toContain('across 40 generated scripts')
  })

  it('renders nothing when there is no shared value', () => {
    render(buildSharedValues([]))
    expect(container.textContent).toBe('')
  })
})

describe('reachLabel', () => {
  it('names the collection-wide reach', () => {
    expect(reachLabel({ occurrence_count: 1, script_names: ['a'] } as SharedValueCandidate))
      .toBe('Used in 1 request')
    expect(reachLabel({ occurrence_count: 40, script_names: ['a', 'b'] } as SharedValueCandidate))
      .toBe('Used in 40 requests across 2 scripts')
  })
})

describe('sharedValueNameError', () => {
  // Mirrors pkg/convert/sharedvalue's parameterNamePattern; a name outside it
  // produces a parameter no {{param.NAME}} template can reference.
  it('rejects names the template grammar cannot address', () => {
    expect(sharedValueNameError('base_url')).toBe('')
    expect(sharedValueNameError('')).not.toBe('')
    expect(sharedValueNameError('2fast')).not.toBe('')
    expect(sharedValueNameError('base-url')).not.toBe('')
  })
})

it('requires a redacted substitution preview and invalidates it when the selection changes', async () => {
  const container = document.createElement('div'); const root = createRoot(container)
  applyMock.mockResolvedValue({ scripts, diffs: [], applied_count: 1 })
  try {
    await act(async () => root.render(<SharedImportValues guided sharedValues={buildSharedValues([candidate({}), candidate({ id: 'missing', name: 'tstamp', kind: 'source_variable' })])} scripts={scripts} onApplied={vi.fn()} />))
    expect(container.textContent).not.toContain('tstamp')
    const checkbox = container.querySelector<HTMLInputElement>('input[type=checkbox]')!
    await act(async () => checkbox.click())
    const button = () => container.querySelector<HTMLButtonElement>('.shared-values-actions button')!
    await act(async () => button().click())
    expect(button().textContent).toBe('Apply reviewed substitutions')
    expect(container.querySelector('[aria-label="Substitution preview"]')?.textContent).toContain('[replacement supplied]')
    expect(container.querySelector('[aria-label="Substitution preview"]')?.textContent).not.toContain('https://stage.example.com')
    await act(async () => checkbox.click())
    expect(container.querySelector('[aria-label="Substitution preview"]')).toBeNull()
    expect(button().disabled).toBe(true)
  } finally { await act(async () => root.unmount()) }
})
