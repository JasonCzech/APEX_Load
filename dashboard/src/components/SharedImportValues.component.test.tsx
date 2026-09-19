// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import SharedImportValues from './SharedImportValues'
import { reachLabel, sharedValueNameError } from './sharedImportValueUtils'
import { type SharedValueCandidate, type SharedValues } from '../types/scriptImports'
import { type ScriptDefinition } from '../types/scripts'

const applyMock = vi.fn()

vi.mock('../hooks/useApi', () => ({
  useApi: () => ({
    applyImportSharedValues: applyMock,
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
  // the panel must not offer to store one.
  it('offers no value input for a credential-shaped candidate', () => {
    render(buildSharedValues([candidate({
      id: 'sv_pw', name: 'password', kind: 'source_variable',
      credential_shaped: true, current_value: undefined,
    })]))
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
