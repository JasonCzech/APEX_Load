// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { HTTPQUICSettingsFields } from './HTTPQUICSettingsFields'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('HTTPQUICSettingsFields', () => {
  let container: HTMLDivElement
  let root: Root
  beforeEach(() => { container = document.createElement('div'); document.body.appendChild(container); root = createRoot(container) })
  afterEach(() => { act(() => root.unmount()); container.remove() })
  const input = (label: string) => Array.from(container.querySelectorAll('label')).find(element => element.querySelector('span')?.textContent === label)!.querySelector('input, select') as HTMLInputElement

  it('edits the five fields and reports out-of-range values at the field', () => {
    const onChange = vi.fn()
    act(() => root.render(<HTTPQUICSettingsFields value={{ handshake_timeout_ms: 500, keep_alive_ms: 1000 }} onChange={onChange} />))
    expect(container.querySelector('legend')?.textContent).toBe('QUIC (HTTP/3)')
    const handshake = input('Handshake timeout (ms)')
    expect(handshake.getAttribute('aria-invalid')).toBe('true')
    const errorId = handshake.getAttribute('aria-describedby')!.split(' ')[1]
    expect(document.getElementById(errorId)?.textContent).toBe('Enter 1,000 to 60,000 ms.')
    expect(input('Max idle timeout (ms)').placeholder).toBe('Default: 30,000')
    act(() => {
      const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!
      setter.call(handshake, '3000')
      handshake.dispatchEvent(new Event('input', { bubbles: true }))
    })
    expect(onChange).toHaveBeenLastCalledWith({ handshake_timeout_ms: 3000 })
    const cache = input('Alt-Svc cache') as unknown as HTMLSelectElement
    act(() => { cache.value = 'per_iteration'; cache.dispatchEvent(new Event('change', { bubbles: true })) })
    expect(onChange).toHaveBeenLastCalledWith({ alt_svc_cache: 'per_iteration' })
    act(() => { cache.value = ''; cache.dispatchEvent(new Event('change', { bubbles: true })) })
    expect(onChange).toHaveBeenLastCalledWith({ alt_svc_cache: undefined })
  })

  it('shows inherited values and keeps unknown keys visible', () => {
    act(() => root.render(<HTTPQUICSettingsFields inherited={{ broken_ttl_ms: 60000, alt_svc_cache: 'per_iteration', receive_window: 5 } as never} onChange={vi.fn()} />))
    expect(input('Broken origin TTL (ms)').placeholder).toBe('Inherited: 60,000')
    expect((input('Alt-Svc cache') as unknown as HTMLSelectElement).options[0].textContent).toBe('Inherited (Per iteration)')
    expect(container.querySelector('[role="alert"]')?.textContent).toContain('Not script settings: receive_window')
  })

  it('disables every field with the reason', () => {
    act(() => root.render(<HTTPQUICSettingsFields value={{}} disabledReason="Requests route through the custom proxy." onChange={vi.fn()} />))
    expect(container.querySelector('fieldset')?.disabled).toBe(true)
    expect(container.querySelector('[role="note"]')?.textContent).toBe('Requests route through the custom proxy.')
  })
})
