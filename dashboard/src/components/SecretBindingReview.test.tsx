// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { type SecretBindingRequirement } from '../types/scriptImports'
import SecretBindingReview from './SecretBindingReview'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const requirements: SecretBindingRequirement[] = [
  {
    id: 'authorization-header',
    path: '/actions/0/headers/Authorization',
    label: 'Create Order — Authorization header',
    context: 'Bearer credential',
    suggested_name: 'ORDER_API_TOKEN',
    replacement: 'Bearer {{secret.ORDER_API_TOKEN}}',
  },
  {
    id: 'refresh-token',
    path: '/init/0/params/refresh_token',
    label: 'Refresh session — Refresh token',
    context: 'OAuth refresh credential',
    suggested_name: 'REFRESH_TOKEN',
    replacement: '{{secret.REFRESH_TOKEN}}',
  },
]

function setInput(input: HTMLInputElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')?.set
  setter?.call(input, value)
  input.dispatchEvent(new Event('input', { bubbles: true }))
}

describe('SecretBindingReview', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(() => {
    act(() => root.unmount())
    container.remove()
  })

  function render(onApply = vi.fn()) {
    act(() => {
      root.render(<SecretBindingReview requirements={requirements} onApply={onApply} />)
    })
    return onApply
  }

  it('asks only for accessible logical binding names and shows safe references', () => {
    render()

    expect(container.textContent).toContain('Resolve Redacted Credentials (2)')
    expect(container.textContent).toContain('Saving is disabled')
    expect(container.textContent).toContain('/actions/0/headers/Authorization')
    expect(container.textContent).toContain('{{secret.ORDER_API_TOKEN}}')
    const inputs = Array.from(container.querySelectorAll<HTMLInputElement>('input'))
    expect(inputs.map(input => input.value)).toEqual(['ORDER_API_TOKEN', 'REFRESH_TOKEN'])
    expect(inputs.every(input => input.type === 'text')).toBe(true)
    expect(inputs.every(input => input.labels?.[0]?.textContent === 'Logical binding name')).toBe(true)
    expect(container.querySelector('input[type="password"]')).toBeNull()
  })

  it('emits exact requirement-to-name selections and permits explicit reuse', async () => {
    const onApply = render(vi.fn().mockResolvedValue(undefined))
    const inputs = Array.from(container.querySelectorAll<HTMLInputElement>('input'))

    act(() => setInput(inputs[1], 'ORDER_API_TOKEN'))
    expect(container.querySelectorAll('.secret-binding-review-reuse-badge')).toHaveLength(2)

    const applyButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Apply Bindings'))
    await act(async () => applyButton!.click())

    expect(onApply).toHaveBeenCalledWith([
      { requirement_id: 'authorization-header', secret_name: 'ORDER_API_TOKEN' },
      { requirement_id: 'refresh-token', secret_name: 'ORDER_API_TOKEN' },
    ])
  })

  it('blocks apply and identifies an invalid binding name', () => {
    render()
    const input = container.querySelector<HTMLInputElement>('input')!

    act(() => setInput(input, 'invalid binding value'))

    expect(input.getAttribute('aria-invalid')).toBe('true')
    expect(container.textContent).toContain('Use 1–249 uppercase letters')
    const applyButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Apply Bindings'))
    expect(applyButton?.disabled).toBe(true)
  })

  it('rejects names that cannot fit in a portable secret template', () => {
    render()
    const input = container.querySelector<HTMLInputElement>('input')!

    act(() => setInput(input, 'A'.repeat(250)))

    expect(input.getAttribute('aria-invalid')).toBe('true')
    expect(container.textContent).toContain('Use 1–249 uppercase letters')
  })

  it('announces apply failures without exposing any submitted value', async () => {
    render(vi.fn().mockRejectedValue(new Error('Binding service unavailable')))
    const applyButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Apply Bindings'))

    await act(async () => applyButton!.click())

    const alert = container.querySelector('[role="alert"]')
    expect(alert?.textContent).toContain('Binding service unavailable')
  })
})
