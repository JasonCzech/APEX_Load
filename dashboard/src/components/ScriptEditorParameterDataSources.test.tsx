// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { ParameterDataCellInput } from './ScriptEditorParameterDataSources'

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

describe('ParameterDataCellInput', () => {
  it('keeps keystrokes local and commits the cell once on blur', () => {
    const onCommit = vi.fn()
    act(() => root.render(<ParameterDataCellInput ariaLabel="customer cell" value="alice" onCommit={onCommit} />))
    const input = container.querySelector('input') as HTMLInputElement
    const valueSetter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!

    act(() => {
      input.focus()
      valueSetter.call(input, 'alice-updated')
      input.dispatchEvent(new Event('input', { bubbles: true }))
    })
    expect(input.value).toBe('alice-updated')
    expect(onCommit).not.toHaveBeenCalled()

    act(() => input.blur())
    expect(onCommit).toHaveBeenCalledTimes(1)
    expect(onCommit).toHaveBeenCalledWith('alice-updated')
  })

  it('stages the latest local draft and clears it after commit', () => {
    const onCommit = vi.fn()
    const onDraftChange = vi.fn()
    act(() => root.render(
      <ParameterDataCellInput
        ariaLabel="customer cell"
        value="alice"
        onCommit={onCommit}
        onDraftChange={onDraftChange}
      />,
    ))
    const input = container.querySelector('input') as HTMLInputElement
    const valueSetter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!

    act(() => {
      input.focus()
      valueSetter.call(input, 'alice-updated')
      input.dispatchEvent(new Event('input', { bubbles: true }))
    })
    expect(onDraftChange).toHaveBeenLastCalledWith('alice-updated')

    act(() => input.blur())
    expect(onCommit).toHaveBeenCalledWith('alice-updated')
    expect(onDraftChange).toHaveBeenLastCalledWith(null)
  })

  it('restores the stored value without committing when Escape dismisses an edit', () => {
    const onCommit = vi.fn()
    act(() => root.render(<ParameterDataCellInput ariaLabel="customer cell" value="alice" onCommit={onCommit} />))
    const input = container.querySelector('input') as HTMLInputElement
    const valueSetter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!

    act(() => {
      input.focus()
      valueSetter.call(input, 'discard me')
      input.dispatchEvent(new Event('input', { bubbles: true }))
    })
    act(() => input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true })))

    expect(input.value).toBe('alice')
    expect(document.activeElement).not.toBe(input)
    expect(onCommit).not.toHaveBeenCalled()
  })

  it('commits an active edit when virtualization unmounts the cell without blur', () => {
    const onCommit = vi.fn()
    act(() => root.render(<ParameterDataCellInput ariaLabel="customer cell" value="alice" onCommit={onCommit} />))
    const input = container.querySelector('input') as HTMLInputElement
    const valueSetter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!

    act(() => {
      input.focus()
      valueSetter.call(input, 'alice-scrolled-away')
      input.dispatchEvent(new Event('input', { bubbles: true }))
    })
    expect(onCommit).not.toHaveBeenCalled()

    act(() => root.render(<></>))
    expect(onCommit).toHaveBeenCalledTimes(1)
    expect(onCommit).toHaveBeenCalledWith('alice-scrolled-away')
  })
})
