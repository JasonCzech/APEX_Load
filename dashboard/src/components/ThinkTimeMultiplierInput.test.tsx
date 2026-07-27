// @vitest-environment jsdom

import { act, useState } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import ThinkTimeMultiplierInput from './ThinkTimeMultiplierInput'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

function setNativeValue(input: HTMLInputElement, value: string) {
  Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set?.call(input, value)
  input.dispatchEvent(new Event('input', { bubbles: true }))
}

describe('ThinkTimeMultiplierInput', () => {
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

  it('keeps transient zero text so a tiny positive multiplier can be typed sequentially', () => {
    const onValueChange = vi.fn()
    function Harness() {
      const [value, setValue] = useState<number | undefined>(1)
      return (
        <ThinkTimeMultiplierInput
          value={value}
          onValueChange={next => {
            onValueChange(next)
            setValue(next)
          }}
        />
      )
    }
    act(() => root.render(<Harness />))
    const input = container.querySelector('input') as HTMLInputElement

    act(() => input.dispatchEvent(new FocusEvent('focusin', { bubbles: true })))
    act(() => setNativeValue(input, '0'))
    expect(input.value).toBe('0')
    expect(onValueChange).not.toHaveBeenCalled()

    act(() => setNativeValue(input, '0.05'))
    expect(input.value).toBe('0.05')
    expect(onValueChange).toHaveBeenLastCalledWith(0.05)
  })

  it('treats legacy zero and an invalid committed draft as the default multiplier', () => {
    const onValueChange = vi.fn()
    act(() => root.render(
      <ThinkTimeMultiplierInput value={0} onValueChange={onValueChange} />,
    ))
    const input = container.querySelector('input') as HTMLInputElement
    expect(input.value).toBe('1')
    expect(input.min).toBe('0')
    expect(input.max).toBe('100')
    expect(input.step).toBe('any')

    act(() => input.dispatchEvent(new FocusEvent('focusin', { bubbles: true })))
    act(() => setNativeValue(input, '0'))
    act(() => input.dispatchEvent(new FocusEvent('focusout', { bubbles: true })))
    expect(onValueChange).toHaveBeenLastCalledWith(undefined)
    expect(input.value).toBe('1')
  })

  it('associates its visible label with the generated input id', () => {
    act(() => root.render(
      <ThinkTimeMultiplierInput
        label="Think time multiplier"
        value={1}
        onValueChange={() => undefined}
      />,
    ))
    const label = container.querySelector('label')
    const input = container.querySelector('input')
    expect(label?.htmlFor).toBeTruthy()
    expect(label?.htmlFor).toBe(input?.id)
    expect(label?.textContent).toContain('Think time multiplier')
  })

  it('does not commit a focused draft after the controlled scope changes', () => {
    const onValueChange = vi.fn()
    act(() => root.render(
      <ThinkTimeMultiplierInput value={1} onValueChange={onValueChange} />,
    ))
    const input = container.querySelector('input') as HTMLInputElement

    act(() => input.dispatchEvent(new FocusEvent('focusin', { bubbles: true })))
    act(() => setNativeValue(input, '0'))
    expect(input.value).toBe('0')

    act(() => root.render(
      <ThinkTimeMultiplierInput value={7} onValueChange={onValueChange} />,
    ))
    expect(input.value).toBe('7')

    act(() => input.dispatchEvent(new FocusEvent('focusout', { bubbles: true })))
    expect(onValueChange).not.toHaveBeenCalled()
    expect(input.value).toBe('7')
  })

  it('drops a focused draft when the resource scope changes with the same controlled value', () => {
    const onFirstScopeChange = vi.fn()
    const onSecondScopeChange = vi.fn()
    act(() => root.render(
      <ThinkTimeMultiplierInput
        draftScope="project-1:script-1"
        value={1}
        onValueChange={onFirstScopeChange}
      />,
    ))
    const input = container.querySelector('input') as HTMLInputElement

    act(() => input.dispatchEvent(new FocusEvent('focusin', { bubbles: true })))
    act(() => setNativeValue(input, '0'))
    expect(input.value).toBe('0')

    act(() => root.render(
      <ThinkTimeMultiplierInput
        draftScope="project-2:script-2"
        value={1}
        onValueChange={onSecondScopeChange}
      />,
    ))
    expect(input.value).toBe('1')

    act(() => input.dispatchEvent(new FocusEvent('focusout', { bubbles: true })))
    expect(onFirstScopeChange).not.toHaveBeenCalled()
    expect(onSecondScopeChange).not.toHaveBeenCalled()
    expect(input.value).toBe('1')
  })
})
