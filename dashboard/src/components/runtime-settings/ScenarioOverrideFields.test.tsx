// @vitest-environment jsdom

import { act, useState, type ReactNode } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import {
  RuntimeOverrideField,
  RuntimeOverridePanelList,
  SecretOverrideInput,
  TriStateOverrideSelect,
} from './ScenarioOverrideFields'
import { formatOverrideValue, REDACTED_SECRET } from './scenarioOverrideValues'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

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

function render(node: ReactNode) {
  act(() => root.render(node))
}

function change(element: HTMLInputElement | HTMLSelectElement, value: string) {
  const prototype = element instanceof HTMLSelectElement ? window.HTMLSelectElement.prototype : window.HTMLInputElement.prototype
  const setter = Object.getOwnPropertyDescriptor(prototype, 'value')!.set!
  act(() => {
    setter.call(element, value)
    element.dispatchEvent(new Event(element instanceof HTMLSelectElement ? 'change' : 'input', { bubbles: true }))
  })
}

describe('TriStateOverrideSelect', () => {
  it('labels Inherit with the inherited value and reports undefined, true or false', () => {
    const onChange = vi.fn()
    render(<TriStateOverrideSelect label="Keep-alive" value={undefined} inheritedValue onChange={onChange} />)
    const select = container.querySelector('select') as HTMLSelectElement
    expect(Array.from(select.options).map(option => option.textContent)).toEqual(['Inherit (Enabled)', 'Enabled', 'Disabled'])
    expect(select.value).toBe('')
    change(select, 'false')
    change(select, 'true')
    change(select, '')
    expect(onChange.mock.calls.map(call => call[0])).toEqual([false, true, undefined])
  })

  it('shows an explicit false override apart from inheriting false', () => {
    render(<TriStateOverrideSelect label="Skip TLS verification" value={false} inheritedValue={false} onChange={() => {}} />)
    expect((container.querySelector('select') as HTMLSelectElement).value).toBe('false')
  })
})

describe('SecretOverrideInput', () => {
  it('keeps a stored secret masked, replaces it when typed and clears it with an empty value', () => {
    const onChange = vi.fn()
    render(<SecretOverrideInput label="WS-Security password" value={REDACTED_SECRET} inheritsValue={false} onChange={onChange} />)
    const input = container.querySelector('input') as HTMLInputElement
    expect(input.type).toBe('password')
    expect(input.value).toBe('')
    expect(input.placeholder).toBe('Stored secret (unchanged)')
    change(input, 'typed')
    const clear = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Clear stored secret')
    expect(clear).toBeTruthy()
    act(() => clear!.dispatchEvent(new MouseEvent('click', { bubbles: true })))
    expect(onChange.mock.calls.map(call => call[0])).toEqual(['typed', ''])
  })

  it('restores a stored secret when text typed over it is erased', () => {
    const onChange = vi.fn()
    function Stateful() {
      const [value, setValue] = useState<string | undefined>(REDACTED_SECRET)
      return <SecretOverrideInput label="WS-Security password" value={value} inheritsValue={false} onChange={next => { onChange(next); setValue(next) }} />
    }
    render(<Stateful />)
    const input = container.querySelector('input') as HTMLInputElement
    change(input, 'x')
    change(input, '')
    // The server keeps the stored secret: the mask goes back, not undefined (inherit).
    expect(onChange.mock.calls.map(call => call[0])).toEqual(['x', REDACTED_SECRET])
    expect(input.placeholder).toBe('Stored secret (unchanged)')
    change(input, 'yz')
    change(input, 'y')
    change(input, '')
    expect(onChange).toHaveBeenLastCalledWith(REDACTED_SECRET)
  })

  it('returns an emptied typed value to inherited', () => {
    const onChange = vi.fn()
    render(<SecretOverrideInput label="Password" value="abc" inheritsValue onChange={onChange} />)
    change(container.querySelector('input') as HTMLInputElement, '')
    expect(onChange).toHaveBeenLastCalledWith(undefined)
  })
})

describe('RuntimeOverrideField', () => {
  it('shows the provenance badge always and the reset action only for this layer\'s override', () => {
    const onReset = vi.fn()
    render(
      <>
        <RuntimeOverrideField path="http.request_timeout_ms" label="Request timeout (ms)" provenance="group_override" overridden onReset={onReset}><input /></RuntimeOverrideField>
        <RuntimeOverrideField path="http.connect_timeout_ms" label="Connect timeout (ms)" provenance="script" overridden={false} onReset={() => {}}><input /></RuntimeOverrideField>
      </>,
    )
    const fields = Array.from(container.querySelectorAll('.runtime-override-field'))
    expect(fields.map(field => field.querySelector('.runtime-field-provenance')?.textContent)).toEqual(['Group override', 'Script value'])
    expect(fields[1].querySelector('.runtime-override-reset')).toBeNull()
    const reset = fields[0].querySelector('button[aria-label="Reset Request timeout (ms) to inherited"]')
    expect(reset).toBeTruthy()
    act(() => reset!.dispatchEvent(new MouseEvent('click', { bubbles: true })))
    expect(onReset).toHaveBeenCalledTimes(1)
  })
})

describe('RuntimeOverridePanelList', () => {
  it('renders nothing without entries and resets the clicked path', () => {
    render(<RuntimeOverridePanelList entries={[]} provenance="group_override" onReset={() => {}} />)
    expect(container.innerHTML).toBe('')
    const onReset = vi.fn()
    render(<RuntimeOverridePanelList entries={[{ path: 'http.page_timeout_ms', value: 30000 }, { path: 'http.auth.password', value: REDACTED_SECRET }]} provenance="scenario_default" badgeLabel="Scenario default" onReset={onReset} />)
    expect(Array.from(container.querySelectorAll('.runtime-override-panel-value')).map(value => value.textContent)).toEqual(['30000', 'Stored secret'])
    act(() => container.querySelector('button[aria-label="Reset http.auth.password to inherited"]')!.dispatchEvent(new MouseEvent('click', { bubbles: true })))
    expect(onReset).toHaveBeenCalledWith('http.auth.password')
  })
})

describe('formatOverrideValue', () => {
  it('reads explicit empties, booleans, lists and objects', () => {
    expect(formatOverrideValue(undefined)).toBe('Empty')
    expect(formatOverrideValue(false)).toBe('Disabled')
    expect(formatOverrideValue([])).toBe('None')
    expect(formatOverrideValue(['5xx', '429'])).toBe('5xx, 429')
    expect(formatOverrideValue({ a: 1 })).toBe('{"a":1}')
  })
})
