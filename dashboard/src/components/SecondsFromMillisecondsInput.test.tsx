// @vitest-environment jsdom

import { act, useState } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import SecondsFromMillisecondsInput from './SecondsFromMillisecondsInput'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

function focus(input: HTMLInputElement) {
  input.dispatchEvent(new FocusEvent('focusin', { bubbles: true }))
}

function blur(input: HTMLInputElement) {
  input.dispatchEvent(new FocusEvent('focusout', { bubbles: true }))
}

function typeNumberState(input: HTMLInputElement, key: string, value: string) {
  input.dispatchEvent(new KeyboardEvent('keydown', { bubbles: true, key }))
  Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set?.call(input, value)
  input.dispatchEvent(new InputEvent('input', {
    bubbles: true,
    data: key === 'Backspace' ? null : key,
    inputType: key === 'Backspace' ? 'deleteContentBackward' : 'insertText',
  }))
  input.dispatchEvent(new KeyboardEvent('keyup', { bubbles: true, key }))
}

describe('SecondsFromMillisecondsInput', () => {
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

  it('keeps a focused string draft while a decimal value is typed sequentially', () => {
    const onChangeMs = vi.fn()

    function Harness() {
      const [valueMs, setValueMs] = useState<number | undefined>(5000)
      return (
        <SecondsFromMillisecondsInput
          valueMs={valueMs}
          onChangeMs={next => {
            onChangeMs(next)
            setValueMs(next ?? 0)
          }}
        />
      )
    }

    act(() => root.render(<Harness />))
    const input = container.querySelector('input') as HTMLInputElement

    act(() => focus(input))
    act(() => typeNumberState(input, 'Backspace', ''))
    expect(input.value).toBe('')

    act(() => typeNumberState(input, '2', '2'))
    expect(input.value).toBe('2')

    // Browsers retain the decimal edit buffer for number inputs; jsdom exposes
    // the next valid value once the fractional digit is entered.
    act(() => typeNumberState(input, '.', '2'))
    act(() => typeNumberState(input, '5', '2.5'))
    expect(input.value).toBe('2.5')
    expect(onChangeMs).toHaveBeenLastCalledWith(2500)

    act(() => blur(input))
    expect(input.value).toBe('2.5')
  })

  it('resynchronizes external millisecond changes while unfocused', () => {
    function Harness() {
      const [valueMs, setValueMs] = useState<number | undefined>(1000)
      return (
        <>
          <SecondsFromMillisecondsInput valueMs={valueMs} onChangeMs={setValueMs} />
          <button type="button" onClick={() => setValueMs(2750)}>Update externally</button>
        </>
      )
    }

    act(() => root.render(<Harness />))
    const input = container.querySelector('input') as HTMLInputElement
    expect(input.value).toBe('1')

    act(() => (container.querySelector('button') as HTMLButtonElement).click())
    expect(input.value).toBe('2.75')
  })

  it('keeps the focused draft through an external update and shows that update on blur', () => {
    function Harness() {
      const [valueMs, setValueMs] = useState<number | undefined>(1000)
      return (
        <>
          <SecondsFromMillisecondsInput valueMs={valueMs} onChangeMs={setValueMs} />
          <button type="button" onClick={() => setValueMs(9000)}>Update externally</button>
        </>
      )
    }

    act(() => root.render(<Harness />))
    const input = container.querySelector('input') as HTMLInputElement
    act(() => focus(input))
    act(() => typeNumberState(input, '2', '2'))
    expect(input.value).toBe('2')

    act(() => (container.querySelector('button') as HTMLButtonElement).click())
    expect(input.value).toBe('2')

    act(() => blur(input))
    expect(input.value).toBe('9')
  })

  it('preserves optional empty values and restores a required fallback on blur', () => {
    const optionalChange = vi.fn()
    act(() => root.render(
      <SecondsFromMillisecondsInput
        valueMs={undefined}
        emptyWhenUndefined
        onChangeMs={optionalChange}
      />,
    ))
    const input = container.querySelector('input') as HTMLInputElement
    expect(input.value).toBe('')

    act(() => focus(input))
    act(() => typeNumberState(input, '1', '1'))
    act(() => typeNumberState(input, 'Backspace', ''))
    expect(optionalChange).toHaveBeenLastCalledWith(undefined)
    act(() => blur(input))
    expect(input.value).toBe('')

    function RequiredHarness() {
      const [valueMs, setValueMs] = useState<number | undefined>(5000)
      return (
        <SecondsFromMillisecondsInput
          valueMs={valueMs}
          onChangeMs={next => setValueMs(next ?? 0)}
        />
      )
    }

    act(() => root.render(<RequiredHarness />))
    const requiredInput = container.querySelector('input') as HTMLInputElement
    expect(requiredInput.value).toBe('5')
    act(() => focus(requiredInput))
    act(() => typeNumberState(requiredInput, 'Backspace', ''))
    expect(requiredInput.value).toBe('')
    act(() => blur(requiredInput))
    expect(requiredInput.value).toBe('0')
  })
})
