// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { ExactStringListRows } from './ExactStringListRows'

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

function rowInputs(): HTMLInputElement[] {
  return Array.from(container.querySelectorAll<HTMLInputElement>('input'))
}

function setInputValue(input: HTMLInputElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')!.set!
  setter.call(input, value)
  input.dispatchEvent(new Event('input', { bubbles: true }))
}

function findButton(text: string): HTMLButtonElement | undefined {
  return Array.from(container.querySelectorAll('button')).find(b => (b.textContent || '').trim().includes(text))
}

describe('ExactStringListRows', () => {
  it('keeps an added empty row when the parent re-renders with an equal fresh-identity array', () => {
    const onChange = vi.fn()
    // A parent that always passes a fresh `[]` identity when unset (the real
    // JavaEnvSettingsFields feeder pattern) must not wipe an in-progress row.
    const render = () =>
      act(() => {
        root.render(<ExactStringListRows ariaLabel="cp" values={[]} onChange={onChange} addLabel="Add" />)
      })
    render()
    act(() => findButton('Add')!.click())
    expect(rowInputs().length).toBe(1)
    // Re-render with a new-identity but content-equal array (external rerender).
    render()
    // The uncommitted empty row survives.
    expect(rowInputs().length).toBe(1)
  })

  it('collapses a blanked middle row on blur without leaving a ghost', () => {
    const onChange = vi.fn()
    act(() => {
      root.render(<ExactStringListRows ariaLabel="cp" values={['/a.jar', '/b.jar', '/c.jar']} onChange={onChange} />)
    })
    const inputs = rowInputs()
    expect(inputs.length).toBe(3)
    act(() => setInputValue(inputs[1], ''))
    act(() => inputs[1].dispatchEvent(new FocusEvent('focusout', { bubbles: true })))
    // The blanked row is gone from the UI and the committed value is pruned.
    expect(rowInputs().length).toBe(2)
    expect(onChange).toHaveBeenLastCalledWith(['/a.jar', '/c.jar'])
  })

  it('resets when the parent supplies genuinely different content', () => {
    const onChange = vi.fn()
    const render = (values: string[]) =>
      act(() => {
        root.render(<ExactStringListRows ariaLabel="cp" values={values} onChange={onChange} />)
      })
    render(['/a.jar', '/b.jar'])
    expect(rowInputs().length).toBe(2)
    render(['/a.jar'])
    expect(rowInputs().length).toBe(1)
    expect(rowInputs()[0].value).toBe('/a.jar')
  })

  it('commits a typed value on blur and prunes empties for the parent', () => {
    const onChange = vi.fn()
    act(() => {
      root.render(<ExactStringListRows ariaLabel="cp" values={[]} onChange={onChange} addLabel="Add" />)
    })
    act(() => findButton('Add')!.click())
    const input = rowInputs()[0]
    act(() => setInputValue(input, '/new.jar'))
    act(() => input.dispatchEvent(new FocusEvent('focusout', { bubbles: true })))
    expect(onChange).toHaveBeenLastCalledWith(['/new.jar'])
  })
})
